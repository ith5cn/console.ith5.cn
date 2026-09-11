package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ith5/ith5/internal/audit"
	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/platform/httpx"
	"github.com/ith5/ith5/internal/resources"
)

// ---------------------------------------------------------------
// 组织与策略
// ---------------------------------------------------------------

// listOrganizations 列出当前账号所在的组织；令牌是组织内的，切换组织要重新换会话。
func (s *Server) listOrganizations(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	ms, err := s.Identity.Store().ListMemberships(r.Context(), p.AccountID)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	out := make([]MembershipInfo, 0, len(ms))
	for _, m := range ms {
		if !m.Suspended {
			out = append(out, membershipInfo(m))
		}
	}
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}

// sameOrg 拒绝跨租户的 URL：token 里的 org 才是权威，路径参数必须与之一致。
func (s *Server) sameOrg(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	p := principalFrom(r)
	if chi.URLParam(r, "id") != p.OrgID {
		httpx.WriteError(w, r, s.log, httpx.ErrNotFound)
		return p, false
	}
	return p, true
}

func (s *Server) getOrganization(w http.ResponseWriter, r *http.Request) {
	p, ok := s.sameOrg(w, r)
	if !ok {
		return
	}
	o, err := s.Orgs.Store().GetOrganization(r.Context(), p.OrgID)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	info := OrganizationInfo{ID: o.ID, Name: o.Name, Slug: o.Slug, CreatedAt: o.CreatedAt}
	setETag(w, info)
	httpx.WriteJSON(w, http.StatusOK, info)
}

func (s *Server) updateOrganization(w http.ResponseWriter, r *http.Request) {
	p, ok := s.sameOrg(w, r)
	if !ok {
		return
	}
	var req UpdateOrganizationReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	if cur, err := s.Orgs.Store().GetOrganization(r.Context(), p.OrgID); err == nil && !s.checkIfMatch(w, r, OrganizationInfo{ID: cur.ID, Name: cur.Name, Slug: cur.Slug, CreatedAt: cur.CreatedAt}) {
		return
	}
	if err := s.Orgs.Rename(r.Context(), p.orgActor(r), req.Name); err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	s.getOrganization(w, r)
}

func (s *Server) getPolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.sameOrg(w, r)
	if !ok {
		return
	}
	pol, err := s.Orgs.GetPolicy(r.Context(), p.OrgID)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	setETag(w, policyJSON(pol))
	httpx.WriteJSON(w, http.StatusOK, policyJSON(pol))
}

func (s *Server) putPolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.sameOrg(w, r)
	if !ok {
		return
	}
	var req PolicyJSON
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	pol := organizations.Policy{
		OrgID: p.OrgID, RequiredApprovals: req.RequiredApprovals, LearningsReview: req.LearningsReview,
		ConfidencePrune: req.ConfidencePrune, ConfidencePromote: req.ConfidencePromote, RetentionMonths: req.RetentionMonths,
	}
	if cur, err := s.Orgs.GetPolicy(r.Context(), p.OrgID); err == nil && !s.checkIfMatch(w, r, policyJSON(cur)) {
		return
	}
	if err := s.Orgs.PutPolicy(r.Context(), p.orgActor(r), pol); err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, policyJSON(pol))
}

func policyJSON(p organizations.Policy) PolicyJSON {
	return PolicyJSON{
		RequiredApprovals: p.RequiredApprovals, LearningsReview: p.LearningsReview,
		ConfidencePrune: p.ConfidencePrune, ConfidencePromote: p.ConfidencePromote, RetentionMonths: p.RetentionMonths,
	}
}

// ---------------------------------------------------------------
// 团队
// ---------------------------------------------------------------

func (s *Server) listTeams(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	ts, err := s.Orgs.Store().ListTeams(r.Context(), p.OrgID)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	out := make([]TeamInfo, 0, len(ts))
	for _, t := range ts {
		out = append(out, teamInfo(t))
	}
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}

func (s *Server) getTeam(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	t, err := s.Orgs.Store().GetTeam(r.Context(), p.OrgID, chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	setETag(w, teamInfo(t))
	httpx.WriteJSON(w, http.StatusOK, teamInfo(t))
}

func (s *Server) createTeam(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req CreateTeamReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	id, err := s.Orgs.CreateTeam(r.Context(), p.orgActor(r), req.Name, req.Slug)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	t, err := s.Orgs.Store().GetTeam(r.Context(), p.OrgID, id)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, teamInfo(t))
}

func (s *Server) updateTeam(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req UpdateTeamReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	if cur, err := s.Orgs.Store().GetTeam(r.Context(), p.OrgID, chi.URLParam(r, "id")); err == nil && !s.checkIfMatch(w, r, teamInfo(cur)) {
		return
	}
	if err := s.Orgs.UpdateTeam(r.Context(), p.orgActor(r), chi.URLParam(r, "id"), req.Name, req.Archived); err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	s.getTeam(w, r)
}

// archiveTeam：DELETE 是归档，不物理删除。
func (s *Server) archiveTeam(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	t, err := s.Orgs.Store().GetTeam(r.Context(), p.OrgID, chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	if err := s.Orgs.UpdateTeam(r.Context(), p.orgActor(r), t.ID, t.Name, true); err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "archived"})
}

func (s *Server) putTeamMember(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req PutMemberRoleReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	err := s.Orgs.PutTeamMember(r.Context(), p.orgActor(r), chi.URLParam(r, "id"), chi.URLParam(r, "userID"), organizations.MemberRole(req.Role))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) deleteTeamMember(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if err := s.Orgs.DeleteTeamMember(r.Context(), p.orgActor(r), chi.URLParam(r, "id"), chi.URLParam(r, "userID")); err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listTeamReviewers(w http.ResponseWriter, r *http.Request) {
	s.listReviewers(w, r, organizations.TeamScope(chi.URLParam(r, "id")))
}

func (s *Server) putTeamReviewers(w http.ResponseWriter, r *http.Request) {
	s.putReviewers(w, r, organizations.TeamScope(chi.URLParam(r, "id")))
}

func (s *Server) listProjectReviewers(w http.ResponseWriter, r *http.Request) {
	s.listReviewers(w, r, organizations.ProjectScope(chi.URLParam(r, "id")))
}

func (s *Server) putProjectReviewers(w http.ResponseWriter, r *http.Request) {
	s.putReviewers(w, r, organizations.ProjectScope(chi.URLParam(r, "id")))
}

func (s *Server) listReviewers(w http.ResponseWriter, r *http.Request, sc organizations.Scope) {
	p := principalFrom(r)
	if !p.Subject.Can(organizations.PermProjectRead, sc) {
		httpx.WriteError(w, r, s.log, httpx.ErrNotFound)
		return
	}
	rs, err := s.Orgs.Store().ListReviewers(r.Context(), p.OrgID, sc.Level, sc.OwnerID)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	out := reviewerInfos(rs)
	setETag(w, out)
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}

func reviewerInfos(rs []organizations.Reviewer) []ReviewerInfo {
	out := make([]ReviewerInfo, 0, len(rs))
	for _, x := range rs {
		out = append(out, ReviewerInfo{UserID: x.UserID, Email: x.Email})
	}
	return out
}

func (s *Server) putReviewers(w http.ResponseWriter, r *http.Request, sc organizations.Scope) {
	p := principalFrom(r)
	var req ReviewersReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	if cur, err := s.Orgs.Store().ListReviewers(r.Context(), p.OrgID, sc.Level, sc.OwnerID); err == nil && !s.checkIfMatch(w, r, reviewerInfos(cur)) {
		return
	}
	if err := s.Orgs.PutReviewers(r.Context(), p.orgActor(r), sc, req.UserIDs); err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	s.listReviewers(w, r, sc)
}

func teamInfo(t organizations.Team) TeamInfo {
	out := TeamInfo{ID: t.ID, Name: t.Name, Slug: t.Slug, Archived: t.Archived, CreatedAt: t.CreatedAt}
	for _, m := range t.Members {
		out.Members = append(out.Members, TeamMemberInfo{UserID: m.UserID, Email: m.Email, Name: m.Name, Role: string(m.Role)})
	}
	return out
}

// ---------------------------------------------------------------
// 组织成员
// ---------------------------------------------------------------

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	ms, err := s.Orgs.Store().ListMembers(r.Context(), p.OrgID)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	out := make([]MemberInfo, 0, len(ms))
	for _, m := range ms {
		out = append(out, memberInfo(m))
	}
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}

func (s *Server) getMember(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	m, err := s.Orgs.Store().GetMember(r.Context(), p.OrgID, chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, memberInfo(m))
}

func (s *Server) createMember(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req CreateMemberReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	if len(req.Password) < 12 {
		httpx.WriteError(w, r, s.log, httpx.Validation("密码至少 12 个字符"))
		return
	}
	hash, err := identity.HashPassword(req.Password)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	role := identity.Role(req.Role)
	if role == "" {
		role = identity.RoleMember
	}
	id, err := s.Orgs.CreateLocalMember(r.Context(), p.orgActor(r), req.Email, req.Name, role, hash)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	m, err := s.Orgs.Store().GetMember(r.Context(), p.OrgID, id)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, memberInfo(m))
}

// setMemberStatus 停用会在一个事务里撤销该成员全部设备、凭据与绑定。
func (s *Server) setMemberStatus(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req MemberStatusReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	if !p.Subject.Can(organizations.PermMembershipManage, organizations.OrgScope(p.OrgID)) {
		httpx.WriteError(w, r, s.log, httpx.ErrAccessDenied)
		return
	}
	target := chi.URLParam(r, "id")
	cur, err := s.Orgs.Store().GetMember(r.Context(), p.OrgID, target)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	if cur.Role == identity.RoleOwner && p.Role != identity.RoleOwner {
		httpx.WriteError(w, r, s.log, httpx.ErrAccessDenied)
		return
	}
	switch req.Status {
	case "suspended":
		if target == p.UserID {
			httpx.WriteError(w, r, s.log, httpx.Validation("不能停用自己"))
			return
		}
		if cur.Role == identity.RoleOwner {
			n, err := s.Orgs.Store().CountOwners(r.Context(), p.OrgID)
			if err != nil {
				httpx.WriteError(w, r, s.log, err)
				return
			}
			if n <= 1 {
				httpx.WriteError(w, r, s.log, mapErr(organizations.ErrLastOwner))
				return
			}
		}
		err = s.Revoker.Suspend(r.Context(), p.OrgID, target)
	case "active":
		err = s.Revoker.Reactivate(r.Context(), p.OrgID, target)
	default:
		httpx.WriteError(w, r, s.log, httpx.Validation("status 只能是 active 或 suspended"))
		return
	}
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	_ = s.Audit.Record(r.Context(), audit.Event{
		OrgID: p.OrgID, ActorUserID: p.UserID, Type: "member.status", TargetType: "user", TargetID: target,
		Detail: map[string]any{"status": req.Status}, RequestID: w.Header().Get("X-Request-Id"),
	})
	s.getMember(w, r)
}

func (s *Server) setMemberRole(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req MemberRoleReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	if err := s.Orgs.SetMemberRole(r.Context(), p.orgActor(r), chi.URLParam(r, "id"), identity.Role(req.Role)); err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	s.getMember(w, r)
}

func (s *Server) resetMemberPassword(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req PasswordReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	if len(req.Password) < 12 {
		httpx.WriteError(w, r, s.log, httpx.Validation("密码至少 12 个字符"))
		return
	}
	hash, err := identity.HashPassword(req.Password)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	if err := s.Orgs.ResetPassword(r.Context(), p.orgActor(r), chi.URLParam(r, "id"), hash); err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func memberInfo(m organizations.Member) MemberInfo {
	status := "active"
	if m.Suspended {
		status = "suspended"
	}
	return MemberInfo{
		UserID: m.UserID, Email: m.Email, Name: m.Name, Role: string(m.Role), Status: status,
		Machines: m.Machines, LastSeenAt: m.LastSeenAt, CreatedAt: m.CreatedAt,
	}
}

// ---------------------------------------------------------------
// 设备
// ---------------------------------------------------------------

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	userID := p.UserID
	if q := r.URL.Query().Get("user_id"); q != "" && q != p.UserID {
		if !p.Subject.Can(organizations.PermDeviceManage, organizations.OrgScope(p.OrgID)) {
			httpx.WriteError(w, r, s.log, httpx.ErrAccessDenied)
			return
		}
		userID = q
	}
	if r.URL.Query().Get("user_id") == "all" && p.Subject.Can(organizations.PermDeviceManage, organizations.OrgScope(p.OrgID)) {
		userID = ""
	}
	ms, err := s.Revoker.Store().ListMachines(r.Context(), p.OrgID, userID)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	out := make([]DeviceInfo, 0, len(ms))
	for _, m := range ms {
		out = append(out, DeviceInfo{
			ID: m.ID, UserID: m.UserID, Email: m.Email, Hostname: m.Hostname, OS: m.OS,
			LastSeenAt: m.LastSeenAt, RevokedAt: m.RevokedAt, CreatedAt: m.CreatedAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}

func (s *Server) renameDevice(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req RenameDeviceReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	owner := p.UserID
	if p.Subject.Can(organizations.PermDeviceManage, organizations.OrgScope(p.OrgID)) {
		owner = ""
	}
	if err := s.Revoker.Store().RenameMachine(r.Context(), p.OrgID, chi.URLParam(r, "id"), owner, req.Hostname); err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// revokeDevice：自己的设备自己撤，组织 admin 可撤任何人的。
func (s *Server) revokeDevice(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id := chi.URLParam(r, "id")
	mc, err := s.Identity.Store().GetMachine(r.Context(), id)
	if err != nil || mc.OrgID != p.OrgID {
		httpx.WriteError(w, r, s.log, httpx.ErrNotFound)
		return
	}
	if mc.UserID != p.UserID && !p.Subject.Can(organizations.PermDeviceManage, organizations.OrgScope(p.OrgID)) {
		httpx.WriteError(w, r, s.log, httpx.ErrAccessDenied)
		return
	}
	if err := s.Revoker.RevokeMachine(r.Context(), p.OrgID, id); err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	_ = s.Audit.Record(r.Context(), audit.Event{
		OrgID: p.OrgID, ActorUserID: p.UserID, Type: "device.revoke", TargetType: "machine", TargetID: id,
		Detail: map[string]any{"user_id": mc.UserID}, RequestID: w.Header().Get("X-Request-Id"),
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// ---------------------------------------------------------------
// 接入码
// ---------------------------------------------------------------

func (s *Server) listEnrollments(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.Subject.Can(organizations.PermMembershipManage, organizations.OrgScope(p.OrgID)) {
		httpx.WriteError(w, r, s.log, httpx.ErrAccessDenied)
		return
	}
	es, err := s.Enrollments.Store().ListEnrollments(r.Context(), p.OrgID)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	out := make([]EnrollmentInfo, 0, len(es))
	for _, e := range es {
		out = append(out, s.enrollmentInfo(e, ""))
	}
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}

// createEnrollment 签发接入码。签发者必须对每个限定项目有管理权限。
func (s *Server) createEnrollment(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var req CreateEnrollmentReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	if len(req.ProjectIDs) == 0 {
		httpx.WriteError(w, r, s.log, httpx.Validation("至少限定一个项目"))
		return
	}
	for _, pid := range req.ProjectIDs {
		if !p.Subject.Can(organizations.PermMembershipManage, organizations.ProjectScope(pid)) {
			httpx.WriteError(w, r, s.log, httpx.ErrAccessDenied)
			return
		}
		if _, err := s.Projects.Store().GetProject(r.Context(), p.OrgID, pid); err != nil {
			httpx.WriteError(w, r, s.log, httpx.Validation("项目不存在: "+pid))
			return
		}
	}
	code, e, err := s.Enrollments.Issue(r.Context(), p.OrgID, p.UserID, req.ProjectIDs, time.Duration(req.TTLHours)*time.Hour, req.MaxUses)
	if err != nil {
		httpx.WriteError(w, r, s.log, httpx.Validation(err.Error()))
		return
	}
	_ = s.Audit.Record(r.Context(), audit.Event{
		OrgID: p.OrgID, ActorUserID: p.UserID, Type: "enrollment.create", TargetType: "enrollment", TargetID: e.ID,
		Detail: map[string]any{"project_ids": e.ProjectIDs, "max_uses": e.MaxUses}, RequestID: w.Header().Get("X-Request-Id"),
	})
	httpx.WriteJSON(w, http.StatusCreated, s.enrollmentInfo(e, code))
}

func (s *Server) revokeEnrollment(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.Subject.Can(organizations.PermMembershipManage, organizations.OrgScope(p.OrgID)) {
		httpx.WriteError(w, r, s.log, httpx.ErrAccessDenied)
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.Enrollments.Store().RevokeEnrollment(r.Context(), p.OrgID, id, time.Now()); err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	_ = s.Audit.Record(r.Context(), audit.Event{
		OrgID: p.OrgID, ActorUserID: p.UserID, Type: "enrollment.revoke", TargetType: "enrollment", TargetID: id,
		RequestID: w.Header().Get("X-Request-Id"),
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// enrollmentInfo 渲染接入码；code 非空时附上给员工的完整接入命令。
func (s *Server) enrollmentInfo(e identity.Enrollment, code string) EnrollmentInfo {
	out := EnrollmentInfo{
		ID: e.ID, Code: code, ProjectIDs: e.ProjectIDs, CreatedBy: e.CreatedBy, ExpiresAt: e.ExpiresAt,
		MaxUses: e.MaxUses, UsedCount: e.UsedCount, RevokedAt: e.RevokedAt, CreatedAt: e.CreatedAt,
	}
	if code != "" {
		out.Command = "teamai init --server " + s.BaseURL + " --code " + code
	}
	return out
}

// ---------------------------------------------------------------
// 身份源
// ---------------------------------------------------------------

func (s *Server) requireIdentityManage(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	p := principalFrom(r)
	if !p.Subject.Can(organizations.PermIdentityManage, organizations.OrgScope(p.OrgID)) {
		httpx.WriteError(w, r, s.log, httpx.ErrAccessDenied)
		return p, false
	}
	return p, true
}

// idpConfigJSON 读出当前身份源配置；未配置时返回一张空表单（scopes 为空数组）。
func (s *Server) idpConfigJSON(r *http.Request, orgID string) IdPConfigJSON {
	empty := IdPConfigJSON{Scopes: []string{}}
	if s.OIDC == nil {
		return empty
	}
	c, err := s.oidcStore().GetOIDCConfig(r.Context(), orgID)
	if err != nil {
		return empty
	}
	scopes := c.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return IdPConfigJSON{ID: c.ID, Issuer: c.Issuer, ClientID: c.ClientID, Scopes: scopes, Enabled: c.Enabled}
}

func (s *Server) getIdPConfig(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireIdentityManage(w, r)
	if !ok {
		return
	}
	cfg := s.idpConfigJSON(r, p.OrgID)
	setETag(w, cfg)
	httpx.WriteJSON(w, http.StatusOK, cfg)
}

func (s *Server) putIdPConfig(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireIdentityManage(w, r)
	if !ok {
		return
	}
	if s.OIDC == nil {
		httpx.WriteError(w, r, s.log, mapErr(identity.ErrOIDCNotConfigured))
		return
	}
	var req IdPConfigJSON
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	if req.Issuer == "" || req.ClientID == "" {
		httpx.WriteError(w, r, s.log, httpx.Validation("issuer 与 client_id 必填"))
		return
	}
	if !s.checkIfMatch(w, r, s.idpConfigJSON(r, p.OrgID)) {
		return
	}
	id, err := s.oidcStore().PutOIDCConfig(r.Context(), identity.OIDCConfig{
		OrgID: p.OrgID, Issuer: req.Issuer, ClientID: req.ClientID, ClientSecret: req.ClientSecret,
		Scopes: req.Scopes, Enabled: req.Enabled,
	})
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	_ = s.Audit.Record(r.Context(), audit.Event{
		OrgID: p.OrgID, ActorUserID: p.UserID, Type: "idp.config", TargetType: "idp", TargetID: id,
		Detail: map[string]any{"issuer": req.Issuer, "enabled": req.Enabled}, RequestID: w.Header().Get("X-Request-Id"),
	})
	s.getIdPConfig(w, r)
}

func (s *Server) listIdPMappings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireIdentityManage(w, r)
	if !ok {
		return
	}
	if s.OIDC == nil {
		httpx.WriteJSON(w, http.StatusOK, Page[GroupMappingJSON]{Items: []GroupMappingJSON{}})
		return
	}
	ms, err := s.oidcStore().ListGroupMappings(r.Context(), p.OrgID)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	out := mappingJSONs(ms)
	setETag(w, out)
	httpx.WriteJSON(w, http.StatusOK, pageSlice(out, parsePage(r)))
}

func mappingJSONs(ms []identity.GroupMapping) []GroupMappingJSON {
	out := make([]GroupMappingJSON, 0, len(ms))
	for _, m := range ms {
		out = append(out, GroupMappingJSON{IdPGroup: m.IdPGroup, Target: m.Target, TargetID: m.TargetID, Role: m.Role, Priority: m.Priority})
	}
	return out
}

func (s *Server) putIdPMappings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireIdentityManage(w, r)
	if !ok {
		return
	}
	if s.OIDC == nil {
		httpx.WriteError(w, r, s.log, mapErr(identity.ErrOIDCNotConfigured))
		return
	}
	var req struct {
		Items []GroupMappingJSON `json:"items"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	cfg, err := s.oidcStore().GetOIDCConfig(r.Context(), p.OrgID)
	if err != nil {
		httpx.WriteError(w, r, s.log, httpx.Validation("请先配置身份源"))
		return
	}
	if cur, err := s.oidcStore().ListGroupMappings(r.Context(), p.OrgID); err == nil && !s.checkIfMatch(w, r, mappingJSONs(cur)) {
		return
	}
	ms := make([]identity.GroupMapping, 0, len(req.Items))
	for _, m := range req.Items {
		if m.IdPGroup == "" || m.Role == "" {
			httpx.WriteError(w, r, s.log, httpx.Validation("idp_group 与 role 必填"))
			return
		}
		switch resources.Level(m.Target) {
		case resources.LevelOrg:
			if !validOrgRole(m.Role) {
				httpx.WriteError(w, r, s.log, httpx.Validation("组织角色只能是 owner/admin/member/viewer"))
				return
			}
			m.TargetID = ""
		case resources.LevelTeam, resources.LevelProject:
			if m.TargetID == "" || !organizations.MemberRole(m.Role).Valid() {
				httpx.WriteError(w, r, s.log, httpx.Validation("团队/项目映射需要 target_id，角色只能是 admin/member"))
				return
			}
		default:
			httpx.WriteError(w, r, s.log, httpx.Validation("target 只能是 org、team 或 project"))
			return
		}
		ms = append(ms, identity.GroupMapping{IdPGroup: m.IdPGroup, Target: m.Target, TargetID: m.TargetID, Role: m.Role, Priority: m.Priority})
	}
	if err := s.oidcStore().PutGroupMappings(r.Context(), p.OrgID, cfg.ID, ms); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	_ = s.Audit.Record(r.Context(), audit.Event{
		OrgID: p.OrgID, ActorUserID: p.UserID, Type: "idp.mappings", TargetType: "idp", TargetID: cfg.ID,
		Detail: map[string]any{"count": len(ms)}, RequestID: w.Header().Get("X-Request-Id"),
	})
	s.listIdPMappings(w, r)
}

func validOrgRole(r string) bool {
	switch identity.Role(r) {
	case identity.RoleOwner, identity.RoleAdmin, identity.RoleMember, identity.RoleViewer:
		return true
	}
	return false
}

// ---------------------------------------------------------------
// 审计
// ---------------------------------------------------------------

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.Subject.Can(organizations.PermAuditRead, organizations.OrgScope(p.OrgID)) {
		httpx.WriteError(w, r, s.log, httpx.ErrAccessDenied)
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	query := audit.Query{OrgID: p.OrgID, Type: q.Get("type"), Actor: q.Get("actor"), Limit: limit, Cursor: q.Get("cursor")}
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			query.From = t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			query.To = t
		}
	}
	events, next, err := s.Audit.List(r.Context(), query)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	out := make([]AuditEventInfo, 0, len(events))
	for _, e := range events {
		out = append(out, AuditEventInfo{
			ID: e.ID, ActorEmail: e.ActorEmail, Type: e.Type, TargetType: e.TargetType, TargetID: e.TargetID,
			Detail: e.Detail, RequestID: e.RequestID, OccurredAt: e.OccurredAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, Page[AuditEventInfo]{Items: out, NextCursor: next})
}
