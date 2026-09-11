package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/platform/httpx"
	"github.com/ith5/ith5/internal/projects"
)

// ---------------------------------------------------------------
// 项目
// ---------------------------------------------------------------

// listProjects 只返回调用方有读权限的项目。
func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	ps, err := s.Projects.Store().ListProjects(r.Context(), p.OrgID)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	out := make([]ProjectInfo, 0, len(ps))
	for _, x := range ps {
		if p.Subject.Can(organizations.PermProjectRead, organizations.ProjectScope(x.ID)) {
			out = append(out, projectInfo(x))
		}
	}
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id := chi.URLParam(r, "id")
	if !p.Subject.Can(organizations.PermProjectRead, organizations.ProjectScope(id)) {
		httpx.WriteError(w, r, s.log, httpx.ErrNotFound)
		return
	}
	x, err := s.Projects.Store().GetProject(r.Context(), p.OrgID, id)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	setETag(w, projectInfo(x))
	httpx.WriteJSON(w, http.StatusOK, projectInfo(x))
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req CreateProjectReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	id, err := s.Projects.Create(r.Context(), p.projectActor(r), req.Name, req.Slug, req.TeamID)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	x, err := s.Projects.Store().GetProject(r.Context(), p.OrgID, id)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, projectInfo(x))
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req UpdateProjectReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	id := chi.URLParam(r, "id")
	if cur, err := s.Projects.Store().GetProject(r.Context(), p.OrgID, id); err == nil && !s.checkIfMatch(w, r, projectInfo(cur)) {
		return
	}
	if err := s.Projects.Update(r.Context(), p.projectActor(r), id, req.Name, req.TeamID, req.Archived); err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	x, err := s.Projects.Store().GetProject(r.Context(), p.OrgID, id)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, projectInfo(x))
}

func (s *Server) putProjectMember(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req PutMemberRoleReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	err := s.Projects.PutMember(r.Context(), p.projectActor(r), chi.URLParam(r, "id"), chi.URLParam(r, "userID"), organizations.MemberRole(req.Role))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) deleteProjectMember(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if err := s.Projects.DeleteMember(r.Context(), p.projectActor(r), chi.URLParam(r, "id"), chi.URLParam(r, "userID")); err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func projectInfo(x projects.Project) ProjectInfo {
	out := ProjectInfo{ID: x.ID, TeamID: x.TeamID, Name: x.Name, Slug: x.Slug, Archived: x.Archived, CreatedAt: x.CreatedAt}
	for _, m := range x.Members {
		out.Members = append(out.Members, TeamMemberInfo{UserID: m.UserID, Email: m.Email, Name: m.Name, Role: string(m.Role)})
	}
	return out
}

// ---------------------------------------------------------------
// 绑定
// ---------------------------------------------------------------

func (s *Server) createBinding(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req BindingReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	b, err := s.Projects.Bind(r.Context(), p.projectActor(r), req.WorkspaceID, req.DisplayName, req.ProjectIDs)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	w.Header().Set("ETag", quote(b.ETag))
	httpx.WriteJSON(w, http.StatusCreated, bindingInfo(b))
}

func (s *Server) listBindings(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	userID := p.UserID
	if q := r.URL.Query().Get("user_id"); q != "" && q != p.UserID {
		if !p.Subject.Can(organizations.PermDeviceManage, organizations.OrgScope(p.OrgID)) {
			httpx.WriteError(w, r, s.log, httpx.ErrAccessDenied)
			return
		}
		userID = q
		if q == "all" {
			userID = ""
		}
	}
	bs, err := s.Projects.Store().ListBindings(r.Context(), p.OrgID, userID)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	out := make([]BindingInfo, 0, len(bs))
	for _, b := range bs {
		out = append(out, bindingInfo(b))
	}
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}

// ownBinding 读取绑定并确认归属：自己的，或组织 admin。
func (s *Server) ownBinding(w http.ResponseWriter, r *http.Request) (projects.Binding, bool) {
	p := principalFrom(r)
	b, err := s.Projects.Store().GetBinding(r.Context(), p.OrgID, chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return b, false
	}
	if b.UserID != p.UserID && !p.Subject.Can(organizations.PermDeviceManage, organizations.OrgScope(p.OrgID)) {
		httpx.WriteError(w, r, s.log, httpx.ErrNotFound)
		return b, false
	}
	return b, true
}

func (s *Server) getBinding(w http.ResponseWriter, r *http.Request) {
	b, ok := s.ownBinding(w, r)
	if !ok {
		return
	}
	w.Header().Set("ETag", quote(b.ETag))
	httpx.WriteJSON(w, http.StatusOK, bindingInfo(b))
}

// updateBinding 需要 If-Match；缺失 428，过期 412。
func (s *Server) updateBinding(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	ifMatch := unquote(r.Header.Get("If-Match"))
	if ifMatch == "" {
		httpx.WriteError(w, r, s.log, httpx.New(http.StatusPreconditionRequired, httpx.CodePreconditionRequired, "需要 If-Match"))
		return
	}
	var req BindingReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	b, err := s.Projects.Rebind(r.Context(), p.projectActor(r), chi.URLParam(r, "id"), ifMatch, req.DisplayName, req.ProjectIDs)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	w.Header().Set("ETag", quote(b.ETag))
	httpx.WriteJSON(w, http.StatusOK, bindingInfo(b))
}

func (s *Server) deleteBinding(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if err := s.Projects.Unbind(r.Context(), p.projectActor(r), chi.URLParam(r, "id")); err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func bindingInfo(b projects.Binding) BindingInfo {
	return BindingInfo{
		ID: b.ID, MachineID: b.MachineID, WorkspaceID: b.WorkspaceID, DisplayName: b.DisplayName,
		ProjectIDs: b.ProjectIDs, AppliedRevision: b.AppliedRevision, State: string(b.State),
		LastSyncAt: b.LastSyncAt, UpdatedAt: b.UpdatedAt, ETag: b.ETag,
	}
}

func quote(s string) string { return `"` + s + `"` }

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
