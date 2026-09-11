package api

import (
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/ith5/ith5/internal/audit"
	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/platform/httpx"
)

// ---------------------------------------------------------------
// IdP 对账
// ---------------------------------------------------------------

// ReconcileResp 是对账报告：与当前映射表不一致的成员及差异。
type ReconcileResp struct {
	Items []identity.ReconcileDiff `json:"items"`
}

// ReconcileApplyReq 选择要应用的成员；为空表示全部。
type ReconcileApplyReq struct {
	UserIDs []string `json:"user_ids,omitempty"`
}

func (s *Server) reconcileDiffs(r *http.Request, orgID string) ([]identity.ReconcileDiff, error) {
	store := s.oidcStore()
	mappings, err := store.ListGroupMappings(r.Context(), orgID)
	if err != nil {
		return nil, err
	}
	members, err := s.Reconcile.ListOIDCMembers(r.Context(), orgID)
	if err != nil {
		return nil, err
	}
	return identity.Reconcile(mappings, members), nil
}

// getIdPReconcile 只生成报告，不改任何数据。
func (s *Server) getIdPReconcile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireIdentityManage(w, r)
	if !ok {
		return
	}
	if s.Reconcile == nil {
		httpx.WriteJSON(w, http.StatusOK, ReconcileResp{Items: []identity.ReconcileDiff{}})
		return
	}
	diffs, err := s.reconcileDiffs(r, p.OrgID)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	if diffs == nil {
		diffs = []identity.ReconcileDiff{}
	}
	httpx.WriteJSON(w, http.StatusOK, ReconcileResp{Items: diffs})
}

// applyIdPReconcile 应用报告里的差异；管理员确认后才调用，每个成员记一条审计。
func (s *Server) applyIdPReconcile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireIdentityManage(w, r)
	if !ok {
		return
	}
	if s.Reconcile == nil {
		httpx.WriteError(w, r, s.log, httpx.Validation("未配置身份源"))
		return
	}
	var req ReconcileApplyReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	diffs, err := s.reconcileDiffs(r, p.OrgID)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	only := map[string]bool{}
	for _, id := range req.UserIDs {
		only[id] = true
	}
	applied := 0
	for _, d := range diffs {
		if len(only) > 0 && !only[d.UserID] {
			continue
		}
		if err := s.Reconcile.ApplyMembership(r.Context(), p.OrgID, d.UserID, d.OrgRole, d.TeamRoles, d.ProjectRoles); err != nil {
			httpx.WriteError(w, r, s.log, err)
			return
		}
		_ = s.Audit.Record(r.Context(), audit.Event{
			OrgID: p.OrgID, ActorUserID: p.UserID, Type: "idp.reconcile", TargetType: "user", TargetID: d.UserID,
			Detail: map[string]any{"changes": d.Changes}, RequestID: middleware.GetReqID(r.Context()),
		})
		applied++
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]int{"applied": applied})
}

// ---------------------------------------------------------------
// 维护任务
// ---------------------------------------------------------------

// runMaintenance 让 owner 立刻对本组织跑一轮保留期清理，不等每日定时。
func (s *Server) runMaintenance(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.Subject.Can(organizations.PermOrganizationManage, organizations.OrgScope(p.OrgID)) {
		httpx.WriteError(w, r, s.log, httpx.ErrAccessDenied)
		return
	}
	if s.Janitor == nil {
		httpx.WriteError(w, r, s.log, httpx.Validation("未启用维护任务"))
		return
	}
	rep := s.Janitor.RunOnce(r.Context(), p.OrgID)
	_ = s.Audit.Record(r.Context(), audit.Event{
		OrgID: p.OrgID, ActorUserID: p.UserID, Type: "maintenance.run", TargetType: "organization", TargetID: p.OrgID,
		Detail: map[string]any{"deleted": rep.Deleted, "errors": rep.Errors}, RequestID: middleware.GetReqID(r.Context()),
	})
	httpx.WriteJSON(w, http.StatusOK, rep)
}
