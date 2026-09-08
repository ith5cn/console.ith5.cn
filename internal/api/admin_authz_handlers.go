package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ith5/ith5/internal/auth"
	"github.com/ith5/ith5/internal/core"
	"github.com/ith5/ith5/internal/db"
)

// ---- 权限组 ----

func (s *Server) adminListGroups(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	rows, err := s.db.ListGroups(r.Context(), p.OrgID)
	if err != nil {
		s.internal(w, r, err, "读取权限组")
		return
	}
	out := make([]GroupInfo, 0, len(rows))
	for _, g := range rows {
		out = append(out, GroupInfo{
			ID: g.ID, Key: g.Key, Name: g.Name, Description: g.Description,
			Archived: g.Archived, BundleIDs: g.BundleIDs, BundleNames: g.BundleNames,
			AssignedTo: g.AssignedTo,
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}

func (s *Server) adminCreateGroup(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	var req struct {
		Key         string `json:"key"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	if req.Key == "" || req.Name == "" {
		s.fail(w, r, http.StatusBadRequest, "bad_request", "key 与 name 必填")
		return
	}
	id, err := s.db.CreateGroup(r.Context(), p.OrgID, req.Key, req.Name, req.Description)
	switch {
	case errors.Is(err, db.ErrConflict):
		s.fail(w, r, http.StatusConflict, "conflict", "同名权限组已存在")
	case err != nil:
		s.internal(w, r, err, "创建权限组")
	default:
		s.writeJSON(w, http.StatusCreated, map[string]string{"id": id})
	}
}

// adminSetGroupBundles 全量替换权限组的成员。
//
// 加进一个 bundle 后，所有被授权者下次 sync 即自动拿到，**不需要改
// 任何一条授权** —— 这是权限组存在的主要理由。
func (s *Server) adminSetGroupBundles(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	var req struct {
		BundleIDs []string `json:"bundle_ids"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	err := s.db.SetGroupBundles(r.Context(), p.OrgID, chi.URLParam(r, "id"), req.BundleIDs)
	switch {
	case errors.Is(err, db.ErrNotFound):
		s.fail(w, r, http.StatusNotFound, "not_found", "权限组不存在")
	case err != nil:
		s.internal(w, r, err, "更新权限组成员")
	default:
		s.writeJSON(w, http.StatusOK, map[string]int{"count": len(req.BundleIDs)})
	}
}

func (s *Server) adminArchiveGroup(w http.ResponseWriter, r *http.Request) {
	s.setArchived(w, r, func(orgID, id string, v bool) error {
		return s.db.SetGroupArchived(r.Context(), orgID, id, v)
	})
}

// ---- 授权 ----

func (s *Server) adminListAssignments(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	rows, err := s.db.ListAssignments(r.Context(), p.OrgID)
	if err != nil {
		s.internal(w, r, err, "读取授权列表")
		return
	}
	now := time.Now()
	out := make([]AssignmentInfo, 0, len(rows))
	for _, a := range rows {
		info := AssignmentInfo{
			ID: a.ID, BundleID: a.BundleID, BundleName: a.BundleName,
			GroupID: a.GroupID, GroupName: a.GroupName,
			SubjectType: a.SubjectType, SubjectID: a.SubjectID,
			SubjectName: a.SubjectName, ExpiresAt: a.ExpiresAt, CreatedAt: a.CreatedAt,
		}
		// 到期不删行（保留审计可追溯性），但列表要标出来
		info.Expired = a.ExpiresAt != nil && !a.ExpiresAt.After(now)
		out = append(out, info)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"assignments": out})
}

func (s *Server) adminCreateAssignment(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	var req struct {
		BundleID    string     `json:"bundle_id"`
		GroupID     string     `json:"group_id"`
		SubjectType string     `json:"subject_type"`
		SubjectID   string     `json:"subject_id"`
		ExpiresAt   *time.Time `json:"expires_at"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	if (req.BundleID == "") == (req.GroupID == "") {
		s.fail(w, r, http.StatusBadRequest, "bad_request", "必须且只能指定 bundle_id 或 group_id 之一")
		return
	}
	switch core.SubjectType(req.SubjectType) {
	case core.SubjectOrg:
		if req.SubjectID != "" {
			s.fail(w, r, http.StatusBadRequest, "bad_request", "org 授权不应带 subject_id")
			return
		}
	case core.SubjectUser, core.SubjectProject:
		if req.SubjectID == "" {
			s.fail(w, r, http.StatusBadRequest, "bad_request", "该授权类型需要 subject_id")
			return
		}
	default:
		s.fail(w, r, http.StatusBadRequest, "bad_request", "subject_type 必须是 org / user / project")
		return
	}

	id, err := s.db.CreateAssignment(r.Context(), p.OrgID,
		req.BundleID, req.GroupID, req.SubjectType, req.SubjectID, req.ExpiresAt)
	if err != nil {
		s.internal(w, r, err, "创建授权")
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) adminDeleteAssignment(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	err := s.db.DeleteAssignment(r.Context(), p.OrgID, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, db.ErrNotFound):
		s.fail(w, r, http.StatusNotFound, "not_found", "不存在")
	case err != nil:
		s.internal(w, r, err, "删除授权")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- 成员、审计、健康、解释器 ----

func (s *Server) adminListMembers(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	rows, err := s.db.ListMembers(r.Context(), p.OrgID)
	if err != nil {
		s.internal(w, r, err, "读取成员")
		return
	}
	out := make([]MemberInfo, 0, len(rows))
	for _, m := range rows {
		out = append(out, MemberInfo{
			ID: m.ID, Email: m.Email, Name: m.Name, Role: m.Role,
			Status: m.Status, Machines: m.Machines, LastSeenAt: m.LastSeenAt,
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

func (s *Server) adminSetMemberStatus(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	var req struct {
		Status string `json:"status"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	if req.Status != "active" && req.Status != "suspended" {
		s.fail(w, r, http.StatusBadRequest, "bad_request", "status 必须是 active 或 suspended")
		return
	}
	if chi.URLParam(r, "id") == p.UserID {
		s.fail(w, r, http.StatusBadRequest, "bad_request", "不能停用自己")
		return
	}
	err := s.db.SetMemberStatus(r.Context(), p.OrgID, chi.URLParam(r, "id"), req.Status)
	switch {
	case errors.Is(err, db.ErrNotFound):
		s.fail(w, r, http.StatusNotFound, "not_found", "不存在")
	case err != nil:
		s.internal(w, r, err, "更新成员状态")
	default:
		s.writeJSON(w, http.StatusOK, map[string]string{"status": req.Status})
	}
}

// adminCreateMember 建号。没有邮件设施，所以走「管理员设初始密码、
// 线下交给员工」这条路：员工拿它 ith5 login 即可，不需要额外的邀请流程。
func (s *Server) adminCreateMember(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	var req struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Role     string `json:"role"`
		Password string `json:"password"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	// 登录是按邮箱精确匹配的，这里统一归一化，免得大写邮箱建完登不进去
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !strings.Contains(email, "@") {
		s.fail(w, r, http.StatusBadRequest, "bad_request", "邮箱格式不正确")
		return
	}
	if req.Role == "" {
		req.Role = "member"
	}
	if req.Role != "member" && req.Role != "admin" && req.Role != "viewer" {
		s.fail(w, r, http.StatusBadRequest, "bad_request", "角色必须是 member、viewer 或 admin")
		return
	}
	// owner 是唯一能扩大管理面的人：admin 不能创建 admin 或只读后台账号。
	if req.Role != "member" && p.Role != "owner" {
		s.fail(w, r, http.StatusForbidden, "forbidden", "只有 owner 能创建后台账号")
		return
	}
	if !validPassword(w, r, s, req.Password) {
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.internal(w, r, err, "生成密码哈希")
		return
	}
	id, err := s.db.CreateMember(r.Context(), p.OrgID, email, strings.TrimSpace(req.Name), req.Role, hash)
	switch {
	case errors.Is(err, db.ErrConflict):
		s.fail(w, r, http.StatusConflict, "conflict", "该邮箱在本组织已有账号")
	case err != nil:
		s.internal(w, r, err, "创建成员")
	default:
		s.writeJSON(w, http.StatusCreated, map[string]string{"id": id})
	}
}

// adminResetMemberPassword 重置成员密码。
//
// 只改密码、不吊销已签发的令牌：这是「忘了密码」的补救，不是「踢下线」——
// 后者是停用要干的事。
func (s *Server) adminResetMemberPassword(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	var req struct {
		Password string `json:"password"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	if !validPassword(w, r, s, req.Password) {
		return
	}
	id := chi.URLParam(r, "id")
	role, err := s.db.MemberRole(r.Context(), p.OrgID, id)
	switch {
	case errors.Is(err, db.ErrNotFound):
		s.fail(w, r, http.StatusNotFound, "not_found", "成员不存在")
		return
	case err != nil:
		s.internal(w, r, err, "读取成员")
		return
	}
	// admin 只能重置普通成员：否则一个 admin 能改掉 owner 的密码，直接夺权
	if role != "member" && p.Role != "owner" {
		s.fail(w, r, http.StatusForbidden, "forbidden", "只有 owner 能重置管理员密码")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.internal(w, r, err, "生成密码哈希")
		return
	}
	switch err := s.db.SetMemberPassword(r.Context(), p.OrgID, id, hash); {
	case errors.Is(err, db.ErrNotFound):
		s.fail(w, r, http.StatusNotFound, "not_found", "成员不存在")
	case err != nil:
		s.internal(w, r, err, "重置密码")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// validPassword 与 init-owner 用同一条下限（12 位），免得后台建出来的号
// 比命令行建的弱。
func validPassword(w http.ResponseWriter, r *http.Request, s *Server, pw string) bool {
	if len([]rune(pw)) < 12 {
		s.fail(w, r, http.StatusBadRequest, "bad_request", "密码至少 12 个字符")
		return false
	}
	return true
}

func (s *Server) adminAudit(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	action := r.URL.Query().Get("action")
	rows, err := s.db.ListDistributions(r.Context(), p.OrgID, action, 200)
	if err != nil {
		s.internal(w, r, err, "读取分发记录")
		return
	}
	out := make([]AuditEntry, 0, len(rows))
	for _, a := range rows {
		out = append(out, AuditEntry{
			Email: a.Email, Hostname: a.Hostname, BundleName: a.BundleName,
			Version: a.Version, Action: a.Action, Detail: a.Detail, CreatedAt: a.CreatedAt,
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

func (s *Server) adminStaleMachines(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	rows, err := s.db.ListStaleMachines(r.Context(), p.OrgID)
	if err != nil {
		s.internal(w, r, err, "读取掉队设备")
		return
	}
	out := make([]StaleMachine, 0, len(rows))
	for _, m := range rows {
		out = append(out, StaleMachine{
			Email: m.Email, Hostname: m.Hostname,
			LastSeenAt: m.LastSeenAt, Days: m.Days, UserLevel: m.UserLevel,
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"machines": out})
}

// adminExplain 是授权解释器：输入一个人，列出他能拿到什么、凭什么拿到。
//
// 管理员最常问的两个问题都靠这一页回答：
//
//	「他为什么能拿到这个」→ 看 via
//	「我授权了他为什么没有」→ 不在列表里，配合 bundle 的归档/发布状态判断
func (s *Server) adminExplain(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		s.fail(w, r, http.StatusBadRequest, "bad_request", "缺少 user_id")
		return
	}
	target, err := s.db.GetUser(r.Context(), userID)
	if err != nil || target.OrgID != p.OrgID {
		s.fail(w, r, http.StatusNotFound, "not_found", "用户不存在")
		return
	}
	projects, err := s.db.GetUserProjects(r.Context(), userID)
	if err != nil {
		s.internal(w, r, err, "读取项目归属")
		return
	}
	data, err := s.db.LoadAuthzData(r.Context(), p.OrgID)
	if err != nil {
		s.internal(w, r, err, "读取授权数据")
		return
	}
	grants := core.Resolve(core.Principal{
		UserID: target.ID, OrgID: target.OrgID, Role: target.Role,
		Suspended: target.Suspended, ProjectIDs: projects,
	}, data.Bundles, data.Groups, data.Assignments, time.Now())

	out := make([]ExplainEntry, 0, len(grants))
	for _, g := range grants {
		e := ExplainEntry{
			BundleID: g.Bundle.ID, BundleName: g.Bundle.Name,
			Kind: string(g.Bundle.Kind), Version: g.Bundle.Version,
		}
		for _, v := range g.Via {
			e.Via = append(e.Via, GrantSourceInfo{
				SubjectType: string(v.SubjectType), GroupName: v.GroupName,
			})
		}
		out = append(out, e)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"user":      UserInfo{ID: target.ID, Email: target.Email, Role: target.Role, OrgID: target.OrgID},
		"suspended": target.Suspended,
		"grants":    out,
	})
}
