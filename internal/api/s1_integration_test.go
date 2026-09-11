package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/ith5/ith5/internal/api"
	"github.com/ith5/ith5/internal/changesets"
	"github.com/ith5/ith5/internal/db"
	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/knowledge"
	"github.com/ith5/ith5/internal/maintenance"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/platform/metrics"
	"github.com/ith5/ith5/internal/platform/pg"
	"github.com/ith5/ith5/internal/projects"
	"github.com/ith5/ith5/internal/releases"
	"github.com/ith5/ith5/internal/sync"
	"github.com/ith5/ith5/internal/telemetry"
)

// S1 的端到端集成测试：真实 PostgreSQL + 完整 HTTP 栈。
// ITH5_TEST_DATABASE_URL 未设置时跳过；每次都重建结构，必须指向专用测试库。

type env struct {
	t                 *testing.T
	srv               http.Handler
	db                *db.DB
	owner             string // owner 会话令牌
	member            string // member 会话令牌
	ownerID, memberID string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dsn := os.Getenv("ITH5_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ITH5_TEST_DATABASE_URL 未设置，跳过集成测试")
	}
	ctx := context.Background()
	if err := pg.Reset(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	hash, _ := identity.HashPassword("demo-password")
	if _, err := d.Bootstrap().SeedDemo(ctx, "demo", "demo@example.com", hash); err != nil {
		t.Fatal(err)
	}
	signer, _ := identity.NewSigner("0123456789abcdef0123456789abcdef")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	orgs := organizations.NewService(d.Organizations(), d.Audit())
	csSvc := changesets.NewService(d.Changesets(), orgs, d.Audit())
	relSvc := releases.NewService(d.Releases(), csSvc, d.Audit())
	deps := api.Deps{
		Identity:    identity.NewService(d.Identity(), signer, "http://localhost").WithEnrollments(d.Enrollments()),
		Enrollments: identity.NewEnrollmentService(d.Enrollments()),
		Revoker:     identity.NewRevoker(d.Suspend()),
		OIDC:        identity.NewOIDC(d.OIDC(), "http://localhost/v1/auth/oidc/callback"),
		OIDCStore:   d.OIDC(),
		Orgs:        orgs,
		Projects:    projects.NewService(d.Projects(), d.Audit()),
		Sync:        sync.NewService(d.Sync()),
		Changesets:  csSvc,
		Releases:    relSvc,
		Resources:   d.Resources(),
		Grants:      d.Grants(),
		Knowledge:   knowledge.NewService(d.Knowledge(), csSvc, relSvc, d.Resources(), orgs, d.Audit()),
		Telemetry:   telemetry.NewService(d.Telemetry()),
		Audit:       d.Audit(),
		Idempotency: d.Idempotency(),
		Reconcile:   d.OIDC(),
		Metrics:     metrics.New(),
		Janitor:     maintenance.New(d.Maintenance(), d.OIDC(), log),
		BaseURL:     "http://localhost",
		Log:         log,
	}
	e := &env{t: t, srv: api.New(deps).Routes(), db: d}
	e.owner, e.ownerID = e.login("demo@example.com")
	e.member, e.memberID = e.login("member@example.com")
	return e
}

func (e *env) do(method, path, token string, body any, headers ...string) (*httptest.ResponseRecorder, map[string]any) {
	e.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func (e *env) login(email string, password ...string) (string, string) {
	e.t.Helper()
	pw := "demo-password"
	if len(password) > 0 {
		pw = password[0]
	}
	rec, body := e.do(http.MethodPost, "/v1/auth/login", "", api.LoginReq{Email: email, Password: pw})
	if rec.Code != http.StatusOK {
		e.t.Fatalf("login %s: %d %s", email, rec.Code, rec.Body)
	}
	userID := body["organizations"].([]any)[0].(map[string]any)["user_id"].(string)
	rec, body = e.do(http.MethodPost, "/v1/auth/session", "", api.SessionReq{LoginToken: body["login_token"].(string), UserID: userID})
	if rec.Code != http.StatusOK {
		e.t.Fatalf("session: %d %s", rec.Code, rec.Body)
	}
	return body["access_token"].(string), userID
}

func (e *env) must(rec *httptest.ResponseRecorder, want int, what string) {
	e.t.Helper()
	if rec.Code != want {
		e.t.Fatalf("%s: got %d want %d: %s", what, rec.Code, want, rec.Body)
	}
}

func TestIntegration_TeamsProjectsAndPermissions(t *testing.T) {
	e := newEnv(t)

	// owner 建团队；member 不能
	rec, _ := e.do(http.MethodPost, "/v1/teams", e.member, api.CreateTeamReq{Name: "X", Slug: "x"})
	e.must(rec, http.StatusForbidden, "member 建团队")
	rec, team := e.do(http.MethodPost, "/v1/teams", e.owner, api.CreateTeamReq{Name: "数据组", Slug: "data"})
	e.must(rec, http.StatusCreated, "owner 建团队")
	teamID := team["id"].(string)
	rec, _ = e.do(http.MethodPost, "/v1/teams", e.owner, api.CreateTeamReq{Name: "again", Slug: "data"})
	e.must(rec, http.StatusConflict, "重复 slug")

	// 把 member 加进团队做 admin，然后 member 能在团队下建项目
	rec, _ = e.do(http.MethodPut, "/v1/teams/"+teamID+"/members/"+e.memberID, e.owner, api.PutMemberRoleReq{Role: "admin"})
	e.must(rec, http.StatusOK, "加团队成员")
	rec, proj := e.do(http.MethodPost, "/v1/projects", e.member, api.CreateProjectReq{Name: "数仓", Slug: "dw", TeamID: teamID})
	e.must(rec, http.StatusCreated, "团队 admin 建项目")
	projID := proj["id"].(string)
	rec, _ = e.do(http.MethodPost, "/v1/projects", e.member, api.CreateProjectReq{Name: "孤儿", Slug: "orphan"})
	e.must(rec, http.StatusForbidden, "member 不能建组织直属项目")

	// /me 的权限列表随角色变化
	rec, me := e.do(http.MethodGet, "/v1/me", e.member, nil)
	e.must(rec, http.StatusOK, "me")
	if perms := me["permissions"].([]any); len(perms) == 0 {
		t.Fatal("member 应有基础权限")
	}

	// 项目列表按可读过滤：member 能看到团队下的 dw 和 seed 里自己所在的 billing，看不到 website
	rec, list := e.do(http.MethodGet, "/v1/projects", e.member, nil)
	e.must(rec, http.StatusOK, "list projects")
	slugs := map[string]bool{}
	for _, it := range list["items"].([]any) {
		slugs[it.(map[string]any)["slug"].(string)] = true
	}
	if !slugs["dw"] || !slugs["billing"] || slugs["website"] {
		t.Fatalf("可见项目集合错误: %v", slugs)
	}

	// reviewer 名单
	rec, _ = e.do(http.MethodPut, "/v1/projects/"+projID+"/reviewers", e.member, api.ReviewersReq{UserIDs: []string{e.ownerID}})
	e.must(rec, http.StatusOK, "指定 reviewer")
	rec, rv := e.do(http.MethodGet, "/v1/projects/"+projID+"/reviewers", e.owner, nil)
	e.must(rec, http.StatusOK, "list reviewers")
	if len(rv["items"].([]any)) != 1 {
		t.Fatalf("reviewer 应有 1 个: %v", rv)
	}

	// 策略：只有 owner 能改；非法值 422
	rec, _ = e.do(http.MethodPut, "/v1/organizations/"+me["membership"].(map[string]any)["org_id"].(string)+"/policy", e.member,
		api.PolicyJSON{RequiredApprovals: 2, ConfidencePrune: 0.1, ConfidencePromote: 0.8, RetentionMonths: 6})
	e.must(rec, http.StatusForbidden, "member 改策略")
	orgID := me["membership"].(map[string]any)["org_id"].(string)
	rec, _ = e.do(http.MethodPut, "/v1/organizations/"+orgID+"/policy", e.owner,
		api.PolicyJSON{RequiredApprovals: 2, ConfidencePrune: 0.9, ConfidencePromote: 0.8, RetentionMonths: 6})
	e.must(rec, http.StatusUnprocessableEntity, "非法策略")
	rec, pol := e.do(http.MethodPut, "/v1/organizations/"+orgID+"/policy", e.owner,
		api.PolicyJSON{RequiredApprovals: 2, ConfidencePrune: 0.1, ConfidencePromote: 0.8, RetentionMonths: 6})
	e.must(rec, http.StatusOK, "改策略")
	if pol["required_approvals"].(float64) != 2 {
		t.Fatalf("策略未生效: %v", pol)
	}

	// 审计里能看到这些动作
	rec, au := e.do(http.MethodGet, "/v1/audit/events?type=team.", e.owner, nil)
	e.must(rec, http.StatusOK, "audit")
	if len(au["items"].([]any)) < 2 {
		t.Fatalf("应记录团队相关审计: %v", au)
	}
	rec, _ = e.do(http.MethodGet, "/v1/audit/events", e.member, nil)
	e.must(rec, http.StatusForbidden, "member 看审计")
}

func TestIntegration_EnrollmentDeviceBindingAndSuspend(t *testing.T) {
	e := newEnv(t)
	// seed：member 是 billing 的成员
	rec, list := e.do(http.MethodGet, "/v1/projects", e.member, nil)
	e.must(rec, http.StatusOK, "projects")
	var billing string
	for _, it := range list["items"].([]any) {
		if m := it.(map[string]any); m["slug"] == "billing" {
			billing = m["id"].(string)
		}
	}

	// owner 签发限定 billing 的接入码
	rec, en := e.do(http.MethodPost, "/v1/enrollments", e.owner, api.CreateEnrollmentReq{ProjectIDs: []string{billing}})
	e.must(rec, http.StatusCreated, "签发接入码")
	code := en["code"].(string)
	if en["command"] == nil {
		t.Fatal("应返回接入命令")
	}

	// 设备流：带接入码开始 → member 在浏览器批准 → 轮询拿到设备令牌与项目
	rec, start := e.do(http.MethodPost, "/v1/auth/device/start", "", api.DeviceStartReq{Fingerprint: "fp-1", Hostname: "dev-box", EnrollmentCode: code})
	e.must(rec, http.StatusOK, "device start")
	rec, _ = e.do(http.MethodPost, "/v1/auth/device/activate", e.member, api.DeviceActivateReq{UserCode: start["user_code"].(string)})
	e.must(rec, http.StatusOK, "activate")
	rec, toks := e.do(http.MethodPost, "/v1/auth/device/poll", "", api.DevicePollReq{DeviceCode: start["device_code"].(string)})
	e.must(rec, http.StatusOK, "poll")
	device := toks["access_token"].(string)
	if ids := toks["enrollment_project_ids"].([]any); len(ids) != 1 || ids[0] != billing {
		t.Fatalf("应带回接入码的项目: %v", toks)
	}

	// 用设备令牌创建绑定；会话令牌不能
	rec, _ = e.do(http.MethodPost, "/v1/bindings", e.member, api.BindingReq{WorkspaceID: "ws-1", ProjectIDs: []string{billing}})
	e.must(rec, http.StatusForbidden, "会话令牌建绑定")
	rec, b := e.do(http.MethodPost, "/v1/bindings", device, api.BindingReq{WorkspaceID: "ws-1", DisplayName: "~/work/billing", ProjectIDs: []string{billing}})
	e.must(rec, http.StatusCreated, "建绑定")
	bindingID := b["id"].(string)
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("绑定响应应带 ETag")
	}

	// PATCH 需要 If-Match；过期 ETag 412
	rec, _ = e.do(http.MethodPatch, "/v1/bindings/"+bindingID, device, api.BindingReq{DisplayName: "x", ProjectIDs: []string{billing}})
	e.must(rec, http.StatusPreconditionRequired, "缺 If-Match")
	rec, _ = e.do(http.MethodPatch, "/v1/bindings/"+bindingID, device, api.BindingReq{DisplayName: "renamed", ProjectIDs: []string{billing}}, "If-Match", etag)
	e.must(rec, http.StatusOK, "PATCH 绑定")
	rec, _ = e.do(http.MethodPatch, "/v1/bindings/"+bindingID, device, api.BindingReq{DisplayName: "again", ProjectIDs: []string{billing}}, "If-Match", etag)
	e.must(rec, http.StatusPreconditionFailed, "过期 ETag")

	// 绑定到无权项目被拒绝
	rec, all := e.do(http.MethodGet, "/v1/projects", e.owner, nil)
	e.must(rec, http.StatusOK, "owner projects")
	var website string
	for _, it := range all["items"].([]any) {
		if m := it.(map[string]any); m["slug"] == "website" {
			website = m["id"].(string)
		}
	}
	rec, _ = e.do(http.MethodPost, "/v1/bindings", device, api.BindingReq{WorkspaceID: "ws-2", ProjectIDs: []string{website}})
	e.must(rec, http.StatusForbidden, "绑定无权项目")

	// 接入码用尽
	rec, _ = e.do(http.MethodPost, "/v1/auth/device/start", "", api.DeviceStartReq{Fingerprint: "fp-2", EnrollmentCode: code})
	e.must(rec, http.StatusUnprocessableEntity, "用尽的接入码")

	// owner 停用 member：设备令牌立刻失效，绑定与设备都标记撤销
	rec, _ = e.do(http.MethodPost, "/v1/members/"+e.memberID+"/status", e.owner, api.MemberStatusReq{Status: "suspended"})
	e.must(rec, http.StatusOK, "停用")
	rec, _ = e.do(http.MethodGet, "/v1/me", device, nil)
	e.must(rec, http.StatusUnauthorized, "停用后设备令牌")
	rec, _ = e.do(http.MethodPost, "/v1/auth/token", "", api.RefreshReq{RefreshToken: toks["refresh_token"].(string)})
	e.must(rec, http.StatusUnauthorized, "停用后刷新")
	rec, devs := e.do(http.MethodGet, "/v1/devices?user_id="+e.memberID, e.owner, nil)
	e.must(rec, http.StatusOK, "devices")
	if d := devs["items"].([]any)[0].(map[string]any); d["revoked_at"] == nil {
		t.Fatalf("设备应被撤销: %v", d)
	}
	rec, bs := e.do(http.MethodGet, "/v1/bindings?user_id="+e.memberID, e.owner, nil)
	e.must(rec, http.StatusOK, "bindings")
	if bb := bs["items"].([]any)[0].(map[string]any); bb["state"] != "revoked" {
		t.Fatalf("绑定应被撤销: %v", bb)
	}

	// 不能停用最后一个 owner，也不能停用自己
	rec, _ = e.do(http.MethodPost, "/v1/members/"+e.ownerID+"/status", e.owner, api.MemberStatusReq{Status: "suspended"})
	e.must(rec, http.StatusUnprocessableEntity, "停用自己")
}

func TestIntegration_MembersAndRoles(t *testing.T) {
	e := newEnv(t)
	rec, m := e.do(http.MethodPost, "/v1/members", e.owner, api.CreateMemberReq{Email: "New@Example.com", Name: "New", Role: "member", Password: "a-long-password"})
	e.must(rec, http.StatusCreated, "邀请成员")
	if m["email"] != "new@example.com" {
		t.Fatalf("邮箱应规范化: %v", m)
	}
	newID := m["user_id"].(string)
	rec, _ = e.do(http.MethodPost, "/v1/members", e.owner, api.CreateMemberReq{Email: "new@example.com", Role: "member", Password: "a-long-password"})
	e.must(rec, http.StatusConflict, "重复邮箱")
	rec, _ = e.do(http.MethodPost, "/v1/members", e.member, api.CreateMemberReq{Email: "x@example.com", Role: "member", Password: "a-long-password"})
	e.must(rec, http.StatusForbidden, "member 邀请")

	// 新成员能登录；升为 admin 后能建团队
	tok, _ := e.login("new@example.com", "a-long-password")
	rec, _ = e.do(http.MethodPost, "/v1/teams", tok, api.CreateTeamReq{Name: "T", Slug: "t"})
	e.must(rec, http.StatusForbidden, "member 建团队")
	rec, _ = e.do(http.MethodPost, "/v1/members/"+newID+"/role", e.owner, api.MemberRoleReq{Role: "admin"})
	e.must(rec, http.StatusOK, "升 admin")
	rec, _ = e.do(http.MethodPost, "/v1/teams", tok, api.CreateTeamReq{Name: "T", Slug: "t"})
	e.must(rec, http.StatusCreated, "admin 建团队")

	// admin 不能造 owner；owner 不能降级最后一个 owner
	rec, _ = e.do(http.MethodPost, "/v1/members/"+e.memberID+"/role", tok, api.MemberRoleReq{Role: "owner"})
	e.must(rec, http.StatusForbidden, "admin 造 owner")
	rec, _ = e.do(http.MethodPost, "/v1/members/"+e.ownerID+"/role", e.owner, api.MemberRoleReq{Role: "admin"})
	e.must(rec, http.StatusConflict, "降级最后 owner")

	// 重置密码后旧密码失效
	rec, _ = e.do(http.MethodPost, "/v1/members/"+newID+"/password", e.owner, api.PasswordReq{Password: "another-long-pw"})
	e.must(rec, http.StatusOK, "重置密码")
	rec, _ = e.do(http.MethodPost, "/v1/auth/login", "", api.LoginReq{Email: "new@example.com", Password: "a-long-password"})
	e.must(rec, http.StatusUnauthorized, "旧密码")
}
