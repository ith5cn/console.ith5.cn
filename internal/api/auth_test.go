package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ith5/ith5/internal/audit"
	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/identity/identitytest"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/projects"
)

const secret = "0123456789abcdef0123456789abcdef"

// stubOrgStore 只实现 LoadSubject：认证中间件需要它，其余方法在本文件的用例里不会被调用。
type stubOrgStore struct {
	organizations.Store
	memberships map[string]identity.Membership
}

func (s stubOrgStore) LoadSubject(_ context.Context, userID string) (organizations.Subject, error) {
	m := s.memberships[userID]
	return organizations.Subject{
		UserID: userID, OrgID: m.OrgID, OrgRole: m.Role,
		TeamRoles: map[string]organizations.MemberRole{}, ProjectRoles: map[string]organizations.MemberRole{},
		ProjectTeam: map[string]string{}, Reviewer: map[string]bool{},
	}, nil
}

type harness struct {
	t      *testing.T
	srv    http.Handler
	store  *identitytest.Store
	userID string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := identitytest.New()
	hash, _ := identity.HashPassword("pw-123456")
	_, userID := store.AddLocalUser("alice@example.com", hash, "org-1", "acme", identity.RoleAdmin)
	signer, _ := identity.NewSigner(secret)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := Deps{
		Identity:    identity.NewService(store, signer, "http://localhost").WithEnrollments(store),
		Enrollments: identity.NewEnrollmentService(store),
		Orgs:        organizations.NewService(stubOrgStore{memberships: store.Memberships}, audit.Nop{}),
		Projects:    projects.NewService(nil, audit.Nop{}),
		BaseURL:     "http://localhost",
		Log:         log,
	}
	return &harness{t: t, srv: New(deps).Routes(), store: store, userID: userID}
}

func (h *harness) do(method, path, token string, body any) (*httptest.ResponseRecorder, map[string]any) {
	h.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func (h *harness) sessionToken() string {
	h.t.Helper()
	rec, body := h.do(http.MethodPost, "/v1/auth/login", "", LoginReq{Email: "alice@example.com", Password: "pw-123456"})
	if rec.Code != http.StatusOK {
		h.t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	rec, body = h.do(http.MethodPost, "/v1/auth/session", "", SessionReq{
		LoginToken: body["login_token"].(string), UserID: h.userID,
	})
	if rec.Code != http.StatusOK {
		h.t.Fatalf("session: %d %s", rec.Code, rec.Body)
	}
	return body["access_token"].(string)
}

func errCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func TestLoginThenSessionThenMe(t *testing.T) {
	h := newHarness(t)
	rec, body := h.do(http.MethodPost, "/v1/auth/login", "", LoginReq{Email: "alice@example.com", Password: "pw-123456"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	if orgs := body["organizations"].([]any); len(orgs) != 1 {
		t.Fatalf("应列出 1 个组织: %v", body)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Fatal("响应应带 X-Request-Id")
	}

	tok := h.sessionToken()
	rec, body = h.do(http.MethodGet, "/v1/me", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}
	if body["token_kind"] != "session" || body["membership"].(map[string]any)["role"] != "admin" {
		t.Fatalf("me 内容错误: %v", body)
	}
	perms := body["permissions"].([]any)
	if len(perms) == 0 {
		t.Fatal("me 应列出权限")
	}
}

func TestLogin_WrongPasswordIs401WithStableCode(t *testing.T) {
	h := newHarness(t)
	rec, body := h.do(http.MethodPost, "/v1/auth/login", "", LoginReq{Email: "alice@example.com", Password: "nope"})
	if rec.Code != http.StatusUnauthorized || errCode(body) != "AUTH_REQUIRED" {
		t.Fatalf("应 401 AUTH_REQUIRED: %d %s", rec.Code, rec.Body)
	}
	rec, _ = h.do(http.MethodPost, "/v1/auth/login", "", LoginReq{Email: "ghost@example.com", Password: "nope"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("账号不存在也应 401: %d", rec.Code)
	}
}

func TestLogin_RejectsUnknownFields(t *testing.T) {
	h := newHarness(t)
	rec, body := h.do(http.MethodPost, "/v1/auth/login", "", map[string]any{"email": "a", "password": "b", "extra": 1})
	if rec.Code != http.StatusUnprocessableEntity || errCode(body) != "VALIDATION_FAILED" {
		t.Fatalf("管理接口应严格解析: %d %s", rec.Code, rec.Body)
	}
}

func TestMe_RequiresTokenAndRejectsSuspended(t *testing.T) {
	h := newHarness(t)
	if rec, _ := h.do(http.MethodGet, "/v1/me", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("无令牌应 401: %d", rec.Code)
	}
	if rec, _ := h.do(http.MethodGet, "/v1/me", "garbage", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("非法令牌应 401: %d", rec.Code)
	}
	tok := h.sessionToken()
	m := h.store.Memberships[h.userID]
	m.Suspended = true
	h.store.Memberships[h.userID] = m
	if rec, _ := h.do(http.MethodGet, "/v1/me", tok, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("停用后未过期的令牌也必须拒绝: %d", rec.Code)
	}
}

func TestDeviceFlowOverHTTP(t *testing.T) {
	h := newHarness(t)
	rec, start := h.do(http.MethodPost, "/v1/auth/device/start", "", DeviceStartReq{Fingerprint: "fp", Hostname: "mac", OS: "darwin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	deviceCode := start["device_code"].(string)
	userCode := start["user_code"].(string)

	rec, body := h.do(http.MethodPost, "/v1/auth/device/poll", "", DevicePollReq{DeviceCode: deviceCode})
	if rec.Code != http.StatusPreconditionRequired || errCode(body) != "AUTHORIZATION_PENDING" {
		t.Fatalf("未批准应 428 pending: %d %s", rec.Code, rec.Body)
	}

	if rec, _ := h.do(http.MethodPost, "/v1/auth/device/activate", "", DeviceActivateReq{UserCode: userCode}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("未登录不能激活: %d", rec.Code)
	}
	session := h.sessionToken()
	rec, peek := h.do(http.MethodGet, "/v1/auth/device/peek?user_code="+userCode, session, nil)
	if rec.Code != http.StatusOK || peek["hostname"] != "mac" || peek["enrollment"] != nil {
		t.Fatalf("peek: %d %s", rec.Code, rec.Body)
	}
	if rec, _ := h.do(http.MethodPost, "/v1/auth/device/activate", session, DeviceActivateReq{UserCode: userCode}); rec.Code != http.StatusOK {
		t.Fatalf("activate: %d %s", rec.Code, rec.Body)
	}

	rec, body = h.do(http.MethodPost, "/v1/auth/device/poll", "", DevicePollReq{DeviceCode: deviceCode})
	if rec.Code != http.StatusOK {
		t.Fatalf("poll: %d %s", rec.Code, rec.Body)
	}
	access := body["access_token"].(string)
	refresh := body["refresh_token"].(string)
	machineID := body["machine_id"].(string)

	rec, me := h.do(http.MethodGet, "/v1/me", access, nil)
	if rec.Code != http.StatusOK || me["token_kind"] != "device" || me["machine_id"] != machineID {
		t.Fatalf("设备令牌应能访问 /me: %d %v", rec.Code, me)
	}
	if rec, _ := h.do(http.MethodPost, "/v1/auth/device/activate", access, DeviceActivateReq{UserCode: "XXXX-2222"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("设备令牌不得激活其他设备: %d", rec.Code)
	}

	rec, body = h.do(http.MethodPost, "/v1/auth/token", "", RefreshReq{RefreshToken: refresh})
	if rec.Code != http.StatusOK || body["refresh_token"] == refresh {
		t.Fatalf("refresh 应轮换: %d %s", rec.Code, rec.Body)
	}
	if rec, _ := h.do(http.MethodPost, "/v1/auth/token", "", RefreshReq{RefreshToken: refresh}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("重用旧凭据应 401: %d", rec.Code)
	}
	if rec, _ := h.do(http.MethodPost, "/v1/auth/revocations", access, RevokeReq{}); rec.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	if h.store.ActiveRefreshCount() != 0 {
		t.Fatal("登出后刷新凭据应全部撤销")
	}
}

func TestDeviceFlow_WithEnrollmentCode(t *testing.T) {
	h := newHarness(t)
	enroll := identity.NewEnrollmentService(h.store)
	code, e, err := enroll.Issue(context.Background(), "org-1", h.userID, []string{"p1"}, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rec, body := h.do(http.MethodPost, "/v1/auth/device/start", "", DeviceStartReq{Fingerprint: "fp", EnrollmentCode: "bogus"}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("无效接入码应 422: %d %v", rec.Code, body)
	}
	rec, start := h.do(http.MethodPost, "/v1/auth/device/start", "", DeviceStartReq{Fingerprint: "fp", EnrollmentCode: code})
	if rec.Code != http.StatusOK {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	session := h.sessionToken()
	userCode := start["user_code"].(string)
	rec, peek := h.do(http.MethodGet, "/v1/auth/device/peek?user_code="+userCode, session, nil)
	en, _ := peek["enrollment"].(map[string]any)
	if rec.Code != http.StatusOK || en == nil || en["id"] != e.ID || en["allowed"] != true {
		// admin 对组织内任何项目都有读权限，所以 allowed 为 true
		t.Fatalf("peek 应显示接入码: %d %s", rec.Code, rec.Body)
	}
	if rec, _ := h.do(http.MethodPost, "/v1/auth/device/activate", session, DeviceActivateReq{UserCode: userCode}); rec.Code != http.StatusOK {
		t.Fatalf("activate: %d %s", rec.Code, rec.Body)
	}
	rec, body := h.do(http.MethodPost, "/v1/auth/device/poll", "", DevicePollReq{DeviceCode: start["device_code"].(string)})
	if rec.Code != http.StatusOK {
		t.Fatalf("poll: %d %s", rec.Code, rec.Body)
	}
	if ids, _ := body["enrollment_project_ids"].([]any); len(ids) != 1 || ids[0] != "p1" {
		t.Fatalf("轮询结果应带接入码限定的项目: %v", body)
	}
	if h.store.Enrollments[e.ID].UsedCount != 1 {
		t.Fatal("批准后接入码应核销一次")
	}
	// 用尽后再发起应被拒绝
	if rec, _ := h.do(http.MethodPost, "/v1/auth/device/start", "", DeviceStartReq{Fingerprint: "fp2", EnrollmentCode: code}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("用尽的接入码应 422: %d", rec.Code)
	}
}

func TestCapabilitiesAndHealth(t *testing.T) {
	h := newHarness(t)
	rec, body := h.do(http.MethodGet, "/v1/capabilities", "", nil)
	if rec.Code != http.StatusOK || body["version"] != Version {
		t.Fatalf("capabilities: %d %s", rec.Code, rec.Body)
	}
	if rec, _ := h.do(http.MethodGet, "/healthz", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("healthz: %d", rec.Code)
	}
	rec, body = h.do(http.MethodGet, "/v1/nope", "", nil)
	if rec.Code != http.StatusNotFound || errCode(body) != "NOT_FOUND" {
		t.Fatalf("未知 /v1 路径应返回结构化 404: %d %s", rec.Code, rec.Body)
	}
}
