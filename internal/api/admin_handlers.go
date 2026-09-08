package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ith5/ith5/internal/auth"
	"github.com/ith5/ith5/internal/core"
	"github.com/ith5/ith5/internal/db"
)

// requireAdmin 校验 Web 用途的令牌，且角色必须是 owner 或 admin。
//
// 与 CLI 面共用「实时查库判断账号状态」的规则：尚未过期的令牌不能让
// 已停用的账号继续访问（D4）。
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if tok == "" {
			s.fail(w, r, http.StatusUnauthorized, "unauthorized", "缺少访问令牌")
			return
		}
		claims, err := s.signer.Verify(tok, auth.PurposeWeb)
		if err != nil {
			s.fail(w, r, http.StatusUnauthorized, "unauthorized", "访问令牌无效或已过期")
			return
		}
		u, err := s.db.GetUser(r.Context(), claims.UserID())
		if err != nil || u.Suspended {
			s.fail(w, r, http.StatusUnauthorized, "unauthorized", "账号不可用")
			return
		}
		if u.Role != "owner" && u.Role != "admin" {
			s.fail(w, r, http.StatusForbidden, "forbidden", "需要管理员权限")
			return
		}
		p := principal{UserID: u.ID, OrgID: u.OrgID, Role: u.Role}
		next.ServeHTTP(w, r.WithContext(withPrincipal(r, p)))
	})
}

// webLogin 是 Web 后台的邮箱密码登录。
func (s *Server) webLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgSlug  string `json:"org_slug"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	if !s.allowPassword(r, req.OrgSlug+"/"+req.Email) {
		w.Header().Set("Retry-After", "60")
		s.fail(w, r, http.StatusTooManyRequests, "rate_limited",
			"该账号的登录尝试过于频繁，请稍后再试")
		return
	}
	user, pwHash, err := s.db.GetUserByEmail(r.Context(), req.OrgSlug, req.Email)
	if err != nil || pwHash == "" {
		s.fail(w, r, http.StatusUnauthorized, "unauthorized", "账号或密码不正确")
		return
	}
	ok, err := auth.VerifyPassword(req.Password, pwHash)
	if err != nil || !ok {
		s.fail(w, r, http.StatusUnauthorized, "unauthorized", "账号或密码不正确")
		return
	}
	if user.Suspended {
		s.fail(w, r, http.StatusForbidden, "suspended", "账号已停用")
		return
	}
	tok, err := s.signer.Issue(user.ID, user.OrgID, user.Role, "", auth.PurposeWeb, time.Now())
	if err != nil {
		s.internal(w, r, err, "签发 Web 令牌")
		return
	}
	s.writeJSON(w, http.StatusOK, TokenResp{
		AccessToken: tok, ExpiresIn: int(auth.AccessTokenTTL.Seconds()),
		User: UserInfo{ID: user.ID, Email: user.Email, Role: user.Role, OrgID: user.OrgID},
	})
}

// ---- Bundle ----

func (s *Server) adminListBundles(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	rows, err := s.db.ListBundles(r.Context(), p.OrgID)
	if err != nil {
		s.internal(w, r, err, "读取 bundle 列表")
		return
	}
	out := make([]BundleSummary, 0, len(rows))
	for _, b := range rows {
		out = append(out, BundleSummary{
			ID: b.ID, Name: b.Name, Kind: b.Kind, Description: b.Description,
			Archived: b.Archived, LatestVersion: b.LatestVersion,
			Checksum: b.Checksum, Groups: b.GroupNames, UpdatedAt: b.UpdatedAt,
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"bundles": out})
}

func (s *Server) adminCreateBundle(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	var req struct {
		Name        string `json:"name"`
		Kind        string `json:"kind"`
		Description string `json:"description"`
		// Content 可选：留空时服务端按 kind 种一份模板，
		// 保证新建出来的编辑器里已有一个合法的 SKILL.md
		Content string `json:"content"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	if !core.Kind(req.Kind).Valid() {
		s.fail(w, r, http.StatusBadRequest, "bad_request", "kind 必须是 skill、command 或 agent")
		return
	}
	id, err := s.db.CreateBundle(r.Context(), p.OrgID, req.Name, req.Kind, req.Description, req.Content)
	switch {
	case errors.Is(err, db.ErrConflict):
		s.fail(w, r, http.StatusConflict, "conflict", err.Error())
		return
	case err != nil && isValidationErr(err):
		s.fail(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	case err != nil:
		s.internal(w, r, err, "创建 bundle")
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) adminGetBundle(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	b, err := s.db.GetBundle(r.Context(), p.OrgID, chi.URLParam(r, "id"))
	if errors.Is(err, db.ErrNotFound) {
		s.fail(w, r, http.StatusNotFound, "not_found", "不存在")
		return
	}
	if err != nil {
		s.internal(w, r, err, "读取 bundle")
		return
	}
	files := make([]FileJSON, len(b.DraftFiles))
	for i, f := range b.DraftFiles {
		files[i] = FileJSON{Path: f.Path, Content: f.Content}
	}
	s.writeJSON(w, http.StatusOK, BundleDetailResp{
		BundleSummary: BundleSummary{
			ID: b.ID, Name: b.Name, Kind: b.Kind, Description: b.Description,
			Archived: b.Archived, LatestVersion: b.LatestVersion,
			Checksum: b.Checksum, UpdatedAt: b.UpdatedAt,
		},
		DraftFiles: files,
	})
}

func (s *Server) adminSaveDraft(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	var req struct {
		Files []FileJSON `json:"files"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	files := make([]core.File, len(req.Files))
	for i, f := range req.Files {
		files[i] = core.File{Path: f.Path, Content: f.Content}
	}
	err := s.db.SaveDraft(r.Context(), p.OrgID, chi.URLParam(r, "id"), files)
	switch {
	case errors.Is(err, db.ErrNotFound):
		s.fail(w, r, http.StatusNotFound, "not_found", "不存在")
	case err != nil && isValidationErr(err):
		s.fail(w, r, http.StatusBadRequest, "bad_request", err.Error())
	case err != nil:
		s.internal(w, r, err, "保存草稿")
	default:
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
	}
}

func (s *Server) adminPublish(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	var req struct {
		Changelog  string `json:"changelog"`
		RollbackOf int    `json:"rollback_of_version"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	v, err := s.db.Publish(r.Context(), p.OrgID, chi.URLParam(r, "id"), p.UserID, req.Changelog, req.RollbackOf)
	switch {
	case errors.Is(err, db.ErrNotFound):
		s.fail(w, r, http.StatusNotFound, "not_found", "不存在")
	case err != nil && isValidationErr(err):
		s.fail(w, r, http.StatusBadRequest, "bad_request", err.Error())
	case err != nil:
		s.internal(w, r, err, "发布")
	default:
		s.writeJSON(w, http.StatusOK, map[string]int{"version": v})
	}
}

func (s *Server) adminListVersions(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	rows, err := s.db.ListVersions(r.Context(), p.OrgID, chi.URLParam(r, "id"))
	if err != nil {
		s.internal(w, r, err, "读取版本历史")
		return
	}
	out := make([]VersionInfo, 0, len(rows))
	for _, v := range rows {
		out = append(out, VersionInfo{
			Version: v.Version, Checksum: v.Checksum, Changelog: v.Changelog,
			RollbackOfVersion: v.RollbackOf, PublishedBy: v.PublishedBy, PublishedAt: v.PublishedAt,
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"versions": out})
}

// adminRollback 把历史版本的内容载入草稿。
//
// 真正的回滚动作是随后的「发布」——用旧内容发一个新版本。这样客户端
// 永远不需要处理降级语义，而 rollback_of_version 让审计能看出这是回滚。
func (s *Server) adminRollback(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	version, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil || version < 1 {
		s.fail(w, r, http.StatusBadRequest, "bad_request", "版本号非法")
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.db.LoadVersionIntoDraft(r.Context(), p.OrgID, id, version); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			s.fail(w, r, http.StatusNotFound, "not_found", "版本不存在")
			return
		}
		s.internal(w, r, err, "载入历史版本")
		return
	}
	newVer, err := s.db.Publish(r.Context(), p.OrgID, id, p.UserID,
		"回滚到 v"+strconv.Itoa(version), version)
	if err != nil {
		s.internal(w, r, err, "发布回滚版本")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]int{"version": newVer, "rollback_of": version})
}

func (s *Server) adminArchiveBundle(w http.ResponseWriter, r *http.Request) {
	s.setArchived(w, r, func(orgID, id string, v bool) error {
		return s.db.SetBundleArchived(r.Context(), orgID, id, v)
	})
}

func (s *Server) setArchived(w http.ResponseWriter, r *http.Request, fn func(string, string, bool) error) {
	p := mustPrincipal(r)
	var req struct {
		Archived bool `json:"archived"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	err := fn(p.OrgID, chi.URLParam(r, "id"), req.Archived)
	switch {
	case errors.Is(err, db.ErrNotFound):
		s.fail(w, r, http.StatusNotFound, "not_found", "不存在")
	case err != nil:
		s.internal(w, r, err, "更新归档状态")
	default:
		s.writeJSON(w, http.StatusOK, map[string]bool{"archived": req.Archived})
	}
}

func isValidationErr(err error) bool {
	for _, target := range []error{
		core.ErrEmptyName, core.ErrNameFormat, core.ErrNameReserved, core.ErrNameTooLong,
		core.ErrNoSkillMD, core.ErrNoAgentMD, core.ErrBadKind,
		core.ErrPathAbsolute, core.ErrPathTraversal, core.ErrPathNUL,
		core.ErrPathBackslash, core.ErrPathNotClean, core.ErrPathDuplicate,
		core.ErrPathEmpty, core.ErrPathHidden,
		core.ErrKindMismatch, core.ErrNoFrontmatter, core.ErrNoDescription, core.ErrBadFrontmatter,
		core.ErrAgentNameMismatch,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
