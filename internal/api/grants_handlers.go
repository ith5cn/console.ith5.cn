package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ith5/ith5/internal/audit"
	"github.com/ith5/ith5/internal/db"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/platform/httpx"
)

// GroupJSON 是权限组。
type GroupJSON struct {
	ID          string    `json:"id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Archived    bool      `json:"archived"`
	BundleIDs   []string  `json:"bundle_ids"`
	BundleNames []string  `json:"bundle_names"`
	AssignedTo  int       `json:"assigned_to"`
	CreatedAt   time.Time `json:"created_at"`
}

// GroupReq 创建或修改权限组。
type GroupReq struct {
	Key         string `json:"key,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Archived    bool   `json:"archived,omitempty"`
}

// GroupBundlesReq 覆盖组内资源。
type GroupBundlesReq struct {
	BundleIDs []string `json:"bundle_ids"`
}

// AssignmentJSON 是一条授权。
type AssignmentJSON struct {
	ID          string     `json:"id"`
	BundleID    string     `json:"bundle_id,omitempty"`
	BundleName  string     `json:"bundle_name,omitempty"`
	GroupID     string     `json:"group_id,omitempty"`
	GroupName   string     `json:"group_name,omitempty"`
	SubjectType string     `json:"subject_type"`
	SubjectID   string     `json:"subject_id,omitempty"`
	SubjectName string     `json:"subject_name,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Expired     bool       `json:"expired"`
	CreatedAt   time.Time  `json:"created_at"`
}

// AssignmentReq 创建授权：bundle_id 与 group_id 二选一。
type AssignmentReq struct {
	BundleID    string     `json:"bundle_id,omitempty"`
	GroupID     string     `json:"group_id,omitempty"`
	SubjectType string     `json:"subject_type"`
	SubjectID   string     `json:"subject_id,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// DistributionJSON 是一条同步回执。
type DistributionJSON struct {
	Email      string         `json:"email"`
	Hostname   string         `json:"hostname"`
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Version    *int           `json:"version,omitempty"`
	Action     string         `json:"action"`
	Detail     map[string]any `json:"detail,omitempty"`
	Revision   string         `json:"revision,omitempty"`
	OccurredAt time.Time      `json:"occurred_at"`
}

// ExecutionJSON 是一条工具调用审计。字段集合即隐私白名单。
type ExecutionJSON struct {
	Email      string         `json:"email"`
	Hostname   string         `json:"hostname"`
	SessionID  string         `json:"session_id"`
	EventType  string         `json:"event_type"`
	ToolName   string         `json:"tool_name,omitempty"`
	Summary    map[string]any `json:"summary"`
	OccurredAt time.Time      `json:"occurred_at"`
}

var groupKeyRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func (s *Server) requireOrgManage(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	p := principalFrom(r)
	if !p.Subject.Can(organizations.PermMembershipManage, organizations.OrgScope(p.OrgID)) {
		httpx.WriteError(w, r, s.log, httpx.ErrAccessDenied)
		return p, false
	}
	return p, true
}

// ---------------------------------------------------------------
// 权限组
// ---------------------------------------------------------------

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	gs, err := s.Grants.ListGroups(r.Context(), p.OrgID)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	out := make([]GroupJSON, 0, len(gs))
	for _, g := range gs {
		out = append(out, groupJSON(g))
	}
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireOrgManage(w, r)
	if !ok {
		return
	}
	var req GroupReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	req.Key, req.Name = strings.TrimSpace(req.Key), strings.TrimSpace(req.Name)
	if !groupKeyRe.MatchString(req.Key) || len(req.Key) > 64 {
		httpx.WriteError(w, r, s.log, httpx.Validation("key 必须是小写字母、数字与连字符"))
		return
	}
	if req.Name == "" || len(req.Name) > 100 {
		httpx.WriteError(w, r, s.log, httpx.Validation("名称长度 1 到 100"))
		return
	}
	id, err := s.Grants.CreateGroup(r.Context(), p.OrgID, req.Key, req.Name, strings.TrimSpace(req.Description))
	if errors.Is(err, db.ErrGroupKeyTaken) {
		httpx.WriteError(w, r, s.log, httpx.StateConflict("key 已存在"))
		return
	}
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	s.auditGrant(r, p, "group.create", "group", id, map[string]any{"key": req.Key})
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) updateGroup(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireOrgManage(w, r)
	if !ok {
		return
	}
	var req GroupReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.Grants.UpdateGroup(r.Context(), p.OrgID, id, strings.TrimSpace(req.Name), strings.TrimSpace(req.Description), req.Archived); err != nil {
		httpx.WriteError(w, r, s.log, mapDBErr(err))
		return
	}
	s.auditGrant(r, p, "group.update", "group", id, map[string]any{"archived": req.Archived})
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) setGroupBundles(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireOrgManage(w, r)
	if !ok {
		return
	}
	var req GroupBundlesReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	id := chi.URLParam(r, "id")
	n, err := s.Grants.SetGroupBundles(r.Context(), p.OrgID, id, req.BundleIDs)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapDBErr(err))
		return
	}
	s.auditGrant(r, p, "group.bundles", "group", id, map[string]any{"count": n})
	httpx.WriteJSON(w, http.StatusOK, map[string]int{"count": n})
}

func groupJSON(g db.Group) GroupJSON {
	if g.BundleIDs == nil {
		g.BundleIDs = []string{}
	}
	if g.BundleNames == nil {
		g.BundleNames = []string{}
	}
	return GroupJSON{ID: g.ID, Key: g.Key, Name: g.Name, Description: g.Description, Archived: g.Archived,
		BundleIDs: g.BundleIDs, BundleNames: g.BundleNames, AssignedTo: g.AssignedTo, CreatedAt: g.CreatedAt}
}

// ---------------------------------------------------------------
// 授权
// ---------------------------------------------------------------

func (s *Server) listAssignments(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	rows, err := s.Grants.ListAssignments(r.Context(), p.OrgID)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	now := time.Now()
	out := make([]AssignmentJSON, 0, len(rows))
	for _, a := range rows {
		out = append(out, AssignmentJSON{
			ID: a.ID, BundleID: a.BundleID, BundleName: a.BundleName, GroupID: a.GroupID, GroupName: a.GroupName,
			SubjectType: a.SubjectType, SubjectID: a.SubjectID, SubjectName: a.SubjectName, ExpiresAt: a.ExpiresAt,
			Expired: a.ExpiresAt != nil && !a.ExpiresAt.After(now), CreatedAt: a.CreatedAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}

func (s *Server) createAssignment(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireOrgManage(w, r)
	if !ok {
		return
	}
	var req AssignmentReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	if (req.BundleID == "") == (req.GroupID == "") {
		httpx.WriteError(w, r, s.log, httpx.Validation("bundle_id 与 group_id 二选一"))
		return
	}
	switch req.SubjectType {
	case "org":
		req.SubjectID = ""
	case "user", "project":
		if req.SubjectID == "" {
			httpx.WriteError(w, r, s.log, httpx.Validation("subject_id 必填"))
			return
		}
	default:
		httpx.WriteError(w, r, s.log, httpx.Validation("subject_type 只能是 org、user 或 project"))
		return
	}
	if req.ExpiresAt != nil && !req.ExpiresAt.After(time.Now()) {
		httpx.WriteError(w, r, s.log, httpx.Validation("expires_at 必须在未来"))
		return
	}
	id, err := s.Grants.CreateAssignment(r.Context(), p.OrgID, req.BundleID, req.GroupID, req.SubjectType, req.SubjectID, req.ExpiresAt)
	switch {
	case errors.Is(err, db.ErrAssignmentExists):
		httpx.WriteError(w, r, s.log, httpx.StateConflict("相同授权已存在"))
		return
	case errors.Is(err, db.ErrAssignmentTarget), errors.Is(err, db.ErrAssignmentSubject):
		httpx.WriteError(w, r, s.log, httpx.Validation(strings.TrimPrefix(err.Error(), "db: ")))
		return
	case err != nil:
		httpx.WriteError(w, r, s.log, err)
		return
	}
	s.auditGrant(r, p, "assignment.create", "assignment", id, map[string]any{
		"bundle_id": req.BundleID, "group_id": req.GroupID, "subject_type": req.SubjectType, "subject_id": req.SubjectID,
	})
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) deleteAssignment(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireOrgManage(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.Grants.DeleteAssignment(r.Context(), p.OrgID, id); err != nil {
		httpx.WriteError(w, r, s.log, mapDBErr(err))
		return
	}
	s.auditGrant(r, p, "assignment.delete", "assignment", id, nil)
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) auditGrant(r *http.Request, p Principal, typ, targetType, targetID string, detail map[string]any) {
	_ = s.Audit.Record(r.Context(), audit.Event{
		OrgID: p.OrgID, ActorUserID: p.UserID, Type: typ, TargetType: targetType, TargetID: targetID,
		Detail: detail, RequestID: r.Header.Get("X-Request-Id"),
	})
}

func mapDBErr(err error) error {
	if errors.Is(err, db.ErrNotFound) {
		return httpx.ErrNotFound
	}
	return err
}

// ---------------------------------------------------------------
// 分发回执与工具调用审计
// ---------------------------------------------------------------

func (s *Server) listDistributions(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.Subject.Can(organizations.PermAuditRead, organizations.OrgScope(p.OrgID)) {
		httpx.WriteError(w, r, s.log, httpx.ErrAccessDenied)
		return
	}
	limit := parsePage(r).dbLimit()
	rows, err := s.Grants.ListDistributions(r.Context(), p.OrgID, r.URL.Query().Get("action"), limit)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	out := make([]DistributionJSON, 0, len(rows))
	for _, d := range rows {
		var detail map[string]any
		_ = json.Unmarshal(d.Detail, &detail)
		out = append(out, DistributionJSON{Email: d.Email, Hostname: d.Hostname, Kind: d.Kind, Name: d.Name, Version: d.Version,
			Action: d.Action, Detail: detail, Revision: d.Revision, OccurredAt: d.OccurredAt})
	}
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}

func (s *Server) listExecutions(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.Subject.Can(organizations.PermAuditRead, organizations.OrgScope(p.OrgID)) {
		httpx.WriteError(w, r, s.log, httpx.ErrAccessDenied)
		return
	}
	q := r.URL.Query()
	limit := parsePage(r).dbLimit()
	rows, err := s.Grants.ListExecutions(r.Context(), p.OrgID, q.Get("user_id"), q.Get("tool"), limit)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	out := make([]ExecutionJSON, 0, len(rows))
	for _, e := range rows {
		var summary map[string]any
		_ = json.Unmarshal(e.Summary, &summary)
		if summary == nil {
			summary = map[string]any{}
		}
		out = append(out, ExecutionJSON{Email: e.Email, Hostname: e.Hostname, SessionID: e.SessionID, EventType: e.EventType,
			ToolName: e.ToolName, Summary: summary, OccurredAt: e.OccurredAt})
	}
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}
