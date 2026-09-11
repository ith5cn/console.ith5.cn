package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ith5/ith5/internal/api"
	"github.com/ith5/ith5/internal/resources"
)

// 同一个 Idempotency-Key 重试创建变更集：拿到同一个变更集而不是两个。
func TestIntegration_IdempotencyKey(t *testing.T) {
	e := newEnv(t)
	billing := e.projectID(e.owner, "billing")
	f := e.upload(e.member, "RULE.md", "# naming\n")
	req := api.ChangesetReq{Title: "idem", Ops: []api.OpJSON{ruleOp("naming", f, billing)}}

	rec, first := e.do(http.MethodPost, "/v1/change-sets", e.member, req, "Idempotency-Key", "k-1")
	e.must(rec, http.StatusCreated, "首次创建")
	if rec.Header().Get("Idempotency-Replayed") != "" {
		t.Fatal("首次请求不该标记为重放")
	}

	rec, again := e.do(http.MethodPost, "/v1/change-sets", e.member, req, "Idempotency-Key", "k-1")
	e.must(rec, http.StatusCreated, "重试")
	if rec.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("重试应标记 Idempotency-Replayed")
	}
	if first["id"] != again["id"] {
		t.Fatalf("重试应返回同一个变更集: %v vs %v", first["id"], again["id"])
	}

	// 同 key 不同请求体 → 409
	other := api.ChangesetReq{Title: "different", Ops: req.Ops}
	rec, body := e.do(http.MethodPost, "/v1/change-sets", e.member, other, "Idempotency-Key", "k-1")
	e.must(rec, http.StatusConflict, "同 key 不同体")
	if errCodeOf(body) != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("错误码 = %s", errCodeOf(body))
	}

	// key 的作用域是 (org, user)：另一个成员用同名 key 互不影响
	rec, third := e.do(http.MethodPost, "/v1/change-sets", e.owner, req, "Idempotency-Key", "k-1")
	e.must(rec, http.StatusCreated, "其他成员同名 key")
	if third["id"] == first["id"] {
		t.Fatal("不同成员的 key 不该共享结果")
	}

	// 列表里只有两个变更集，证明重试没有重复创建
	rec, all := e.do(http.MethodGet, "/v1/change-sets?view=all", e.owner, nil)
	e.must(rec, http.StatusOK, "列表")
	if n := len(all["items"].([]any)); n != 2 {
		t.Fatalf("变更集数量 = %d, 想要 2", n)
	}
}

// 任何 kind 的内容含密钥都在创建变更集时被拦下，不只是 learning。
func TestIntegration_SecretScanOnChangeset(t *testing.T) {
	e := newEnv(t)
	billing := e.projectID(e.owner, "billing")
	f := e.upload(e.member, "RULE.md", "# 部署\n\nexport TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789ABCD\n")
	rec, body := e.do(http.MethodPost, "/v1/change-sets", e.member, api.ChangesetReq{Title: "bad", Ops: []api.OpJSON{ruleOp("deploy", f, billing)}})
	e.must(rec, http.StatusUnprocessableEntity, "含密钥的 rule")
	if errCodeOf(body) != "SECRET_DETECTED" {
		t.Fatalf("错误码 = %s", errCodeOf(body))
	}
	// env：像凭据的名字必须标 secret，且不能带值
	ev := e.upload(e.member, "ENV.yaml", "value: abc123\n")
	rec, body = e.do(http.MethodPost, "/v1/change-sets", e.member, api.ChangesetReq{Title: "env", Ops: []api.OpJSON{{Op: "put", Level: "project", ProjectID: billing, Kind: "env", Name: "API_TOKEN", Files: []resources.FileRef{ev}}}})
	e.must(rec, http.StatusUnprocessableEntity, "未标 secret 的凭据变量")
	if errCodeOf(body) != "VALIDATION_FAILED" {
		t.Fatalf("错误码 = %s", errCodeOf(body))
	}
	ok := e.upload(e.member, "ENV.yaml", "secret: true\ndescription: 网关令牌\n")
	rec, _ = e.do(http.MethodPost, "/v1/change-sets", e.member, api.ChangesetReq{Title: "env", Ops: []api.OpJSON{{Op: "put", Level: "project", ProjectID: billing, Kind: "env", Name: "API_TOKEN", Files: []resources.FileRef{ok}}}})
	e.must(rec, http.StatusCreated, "标了 secret 的凭据变量")
}

// /metrics 暴露按路由模板聚合的计数，不含实际 id。
func TestIntegration_Metrics(t *testing.T) {
	e := newEnv(t)
	rec, _ := e.do(http.MethodGet, "/v1/me", e.owner, nil)
	e.must(rec, http.StatusOK, "me")
	rec, _ = e.do(http.MethodGet, "/metrics", "", nil)
	e.must(rec, http.StatusOK, "metrics")
	body := rec.Body.String()
	if !strings.Contains(body, `route="/v1/me"`) || strings.Contains(body, e.ownerID) {
		t.Fatalf("指标应按路由模板聚合: %s", body[:min(len(body), 400)])
	}
}

// 登录后用登录令牌自建组织：成为 owner，密码在新组织里照常可用，slug 冲突 409。
func TestIntegration_CreateOrganization(t *testing.T) {
	e := newEnv(t)
	rec, login := e.do(http.MethodPost, "/v1/auth/login", "", api.LoginReq{Email: "member@example.com", Password: "demo-password"})
	e.must(rec, http.StatusOK, "login")
	lt := login["login_token"].(string)

	rec, body := e.do(http.MethodPost, "/v1/organizations", "", api.CreateOrganizationReq{LoginToken: lt, Name: "Side Project", Slug: "side"})
	e.must(rec, http.StatusCreated, "create org")
	m := body["membership"].(map[string]any)
	if m["role"] != "owner" || m["org_slug"] != "side" {
		t.Fatalf("membership = %v", m)
	}
	rec, sess := e.do(http.MethodPost, "/v1/auth/session", "", api.SessionReq{LoginToken: lt, UserID: m["user_id"].(string)})
	e.must(rec, http.StatusOK, "session in new org")
	rec, me := e.do(http.MethodGet, "/v1/me", sess["access_token"].(string), nil)
	e.must(rec, http.StatusOK, "me")
	if me["membership"].(map[string]any)["org_slug"] != "side" {
		t.Fatalf("me = %v", me["membership"])
	}
	// 旧密码在新组织仍可登录（哈希已复制）
	rec, again := e.do(http.MethodPost, "/v1/auth/login", "", api.LoginReq{Email: "member@example.com", Password: "demo-password"})
	e.must(rec, http.StatusOK, "relogin")
	if n := len(again["organizations"].([]any)); n != 2 {
		t.Fatalf("应属于 2 个组织，实际 %d", n)
	}
	rec, body = e.do(http.MethodPost, "/v1/organizations", "", api.CreateOrganizationReq{LoginToken: lt, Name: "Dup", Slug: "side"})
	e.must(rec, http.StatusConflict, "duplicate slug")
	if errCodeOf(body) != "STATE_CONFLICT" {
		t.Fatalf("错误码 = %s", errCodeOf(body))
	}
}
