package api

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/platform/httpx"
)

// login 校验密码，返回账号与可进入的组织。令牌是组织内的，由 session 接口签发。
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req LoginReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	email := identity.NormalizeEmail(req.Email)
	if !s.passwordLimit.Allow(httpx.ClientIP(r) + "|" + email) {
		httpx.WriteError(w, r, s.log, httpx.ErrRateLimited)
		return
	}
	res, err := s.Identity.LoginWithPassword(r.Context(), email, req.Password)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	loginTok, err := s.Identity.Signer().IssueLogin(res.Account.ID, time.Now())
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	resp := LoginResp{
		Account:       accountInfo(res.Account),
		LoginToken:    loginTok,
		Organizations: make([]MembershipInfo, 0, len(res.Memberships)),
	}
	for _, m := range res.Memberships {
		resp.Organizations = append(resp.Organizations, membershipInfo(m))
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// session 用登录凭据换取某组织的会话令牌。
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	var req SessionReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	claims, err := s.Identity.Signer().Verify(req.LoginToken, identity.TokenKindLogin)
	if err != nil {
		httpx.WriteError(w, r, s.log, httpx.New(http.StatusUnauthorized, httpx.CodeAuthRequired, "登录凭据无效或已过期"))
		return
	}
	toks, err := s.Identity.IssueSession(r.Context(), claims.Subject, req.UserID)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, tokenResp(toks))
}

// createOrganization 用登录令牌建组织并成为 owner。
// 放在认证入口组：此时还没有组织，也就没有组织内的会话令牌可用。
func (s *Server) createOrganization(w http.ResponseWriter, r *http.Request) {
	var req CreateOrganizationReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	claims, err := s.Identity.Signer().Verify(req.LoginToken, identity.TokenKindLogin)
	if err != nil {
		httpx.WriteError(w, r, s.log, httpx.New(http.StatusUnauthorized, httpx.CodeAuthRequired, "登录凭据无效或已过期"))
		return
	}
	o, userID, err := s.Orgs.CreateOrganization(r.Context(), claims.Subject, req.Name, req.Slug)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, CreateOrganizationResp{
		Organization: OrganizationInfo{ID: o.ID, Name: o.Name, Slug: o.Slug, CreatedAt: o.CreatedAt},
		Membership:   MembershipInfo{UserID: userID, OrgID: o.ID, OrgSlug: o.Slug, OrgName: o.Name, Role: "owner"},
	})
}

// deviceStart 创建设备码。CLI 是终端程序，不适合处理密码，故走设备码。
// 带接入码时先校验接入码可用，把它记在设备码上。
func (s *Server) deviceStart(w http.ResponseWriter, r *http.Request) {
	var req DeviceStartReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	if req.Fingerprint == "" {
		httpx.WriteError(w, r, s.log, httpx.Validation("fingerprint 必填"))
		return
	}
	enrollmentID := ""
	if req.EnrollmentCode != "" {
		e, err := s.Enrollments.Resolve(r.Context(), strings.TrimSpace(req.EnrollmentCode))
		if err != nil {
			httpx.WriteError(w, r, s.log, mapErr(err))
			return
		}
		enrollmentID = e.ID
	}
	res, err := s.Identity.DeviceStart(r.Context(), req.Fingerprint, req.Hostname, req.OS, enrollmentID)
	if err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, DeviceStartResp{
		DeviceCode: res.DeviceCode, UserCode: res.UserCode, VerificationURL: res.VerificationURL,
		Interval: res.Interval, ExpiresIn: res.ExpiresIn,
	})
}

// devicePoll 由 CLI 轮询；批准后签发设备令牌与刷新凭据。
func (s *Server) devicePoll(w http.ResponseWriter, r *http.Request) {
	var req DevicePollReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	toks, err := s.Identity.DevicePoll(r.Context(), req.DeviceCode)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, tokenResp(toks))
}

// devicePeek 让审批页看到它在批准什么：设备信息、接入码限定的项目、当前登录者能否批准。
func (s *Server) devicePeek(w http.ResponseWriter, r *http.Request) {
	dc, e, err := s.Identity.PeekDeviceCode(r.Context(), r.URL.Query().Get("user_code"))
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	resp := DevicePeekResp{Hostname: dc.Hostname, OS: dc.OS, ExpiresAt: dc.ExpiresAt}
	if e != nil {
		p := principalFrom(r)
		resp.Enrollment = &EnrollmentIn{ID: e.ID, ProjectIDs: e.ProjectIDs, Allowed: s.enrollmentAllowed(p, *e)}
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// deviceActivate 是浏览器端的批准：已登录成员输入 CLI 显示的 user_code。
//
// 带接入码的设备码：接入码不授予任何成员身份，批准者必须本来就对那些项目有读权限。
func (s *Server) deviceActivate(w http.ResponseWriter, r *http.Request) {
	var req DeviceActivateReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	p := principalFrom(r)
	_, e, err := s.Identity.PeekDeviceCode(r.Context(), req.UserCode)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	if e != nil && !s.enrollmentAllowed(p, *e) {
		httpx.WriteError(w, r, s.log, httpx.New(http.StatusForbidden, httpx.CodeAccessDenied,
			"接入码限定的项目你没有访问权限，请联系管理员先把你加入项目"))
		return
	}
	if err := s.Identity.DeviceApprove(r.Context(), p.UserID, req.UserCode); err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

func (s *Server) enrollmentAllowed(p Principal, e identity.Enrollment) bool {
	if e.OrgID != p.OrgID {
		return false
	}
	for _, pid := range e.ProjectIDs {
		if !p.Subject.Can(organizations.PermProjectRead, organizations.ProjectScope(pid)) {
			return false
		}
	}
	return true
}

// refresh 轮换刷新凭据。
func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var req RefreshReq
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	toks, err := s.Identity.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, tokenResp(toks))
}

// revoke 撤销自己某台设备的刷新凭据。
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	var req RevokeReq
	if err := httpx.DecodeLenient(w, r, &req); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	p := principalFrom(r)
	machineID := req.MachineID
	if machineID == "" {
		machineID = p.MachineID
	}
	if machineID == "" {
		httpx.WriteError(w, r, s.log, httpx.Validation("machine_id 必填"))
		return
	}
	if err := s.Identity.Logout(r.Context(), p.UserID, machineID); err != nil {
		httpx.WriteError(w, r, s.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// me 返回当前调用方及其组织级权限。
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	perms := p.Subject.Permissions(organizations.OrgScope(p.OrgID))
	out := make([]string, len(perms))
	for i, x := range perms {
		out[i] = string(x)
	}
	httpx.WriteJSON(w, http.StatusOK, MeResp{
		Account:     AccountInfo{ID: p.AccountID, Email: p.Email, Name: p.Name},
		Membership:  membershipInfo(p.Membership),
		MachineID:   p.MachineID,
		TokenKind:   string(p.TokenKind),
		Permissions: out,
	})
}

// ---------------------------------------------------------------
// OIDC（浏览器）
// ---------------------------------------------------------------

// oidcAuthorize 按组织 slug 跳转到该组织的 IdP。
func (s *Server) oidcAuthorize(w http.ResponseWriter, r *http.Request) {
	if s.OIDC == nil {
		httpx.WriteError(w, r, s.log, mapErr(identity.ErrOIDCNotConfigured))
		return
	}
	org, err := s.Orgs.Store().GetOrganizationBySlug(r.Context(), r.URL.Query().Get("org"))
	if err != nil {
		httpx.WriteError(w, r, s.log, httpx.Validation("org 参数无效"))
		return
	}
	u, err := s.OIDC.BeginBrowser(r.Context(), org.ID)
	if err != nil {
		httpx.WriteError(w, r, s.log, mapErr(err))
		return
	}
	http.Redirect(w, r, u, http.StatusFound)
}

// oidcCallback 完成登录，签发会话令牌后把浏览器送回前端。
//
// 令牌放在 URL fragment 里：fragment 不会进服务端日志，也不会随 Referer 泄露。
func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if s.OIDC == nil {
		httpx.WriteError(w, r, s.log, mapErr(identity.ErrOIDCNotConfigured))
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		http.Redirect(w, r, s.BaseURL+"/login?error="+url.QueryEscape(e), http.StatusFound)
		return
	}
	m, err := s.OIDC.CompleteBrowser(r.Context(), q.Get("state"), q.Get("code"))
	if err != nil {
		http.Redirect(w, r, s.BaseURL+"/login?error="+url.QueryEscape(httpx.AsError(mapErr(err)).Message), http.StatusFound)
		return
	}
	toks, err := s.Identity.IssueSession(r.Context(), m.AccountID, m.UserID)
	if err != nil {
		http.Redirect(w, r, s.BaseURL+"/login?error="+url.QueryEscape(httpx.AsError(mapErr(err)).Message), http.StatusFound)
		return
	}
	http.Redirect(w, r, s.BaseURL+"/login/oidc#access_token="+url.QueryEscape(toks.AccessToken), http.StatusFound)
}

func tokenResp(t identity.Tokens) TokenResp {
	return TokenResp{
		AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, TokenType: "Bearer",
		ExpiresIn: t.ExpiresIn, MachineID: t.MachineID, Membership: membershipInfo(t.Membership),
		EnrollmentProjectIDs: t.EnrollmentProjectIDs,
	}
}

func accountInfo(a identity.Account) AccountInfo {
	return AccountInfo{ID: a.ID, Email: a.Email, Name: a.Name}
}

func membershipInfo(m identity.Membership) MembershipInfo {
	return MembershipInfo{UserID: m.UserID, OrgID: m.OrgID, OrgSlug: m.OrgSlug, OrgName: m.OrgName, Role: string(m.Role)}
}
