package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ith5/ith5/internal/api"
	"github.com/ith5/ith5/internal/resources"
	"github.com/ith5/ith5/internal/sync"
	"github.com/ith5/ith5/internal/teamaifmt"
)

// deviceFor 走一遍设备授权流，返回 member 的设备令牌。
func (e *env) deviceFor(sessionToken string) (access string, machineID string) {
	e.t.Helper()
	rec, start := e.do(http.MethodPost, "/v1/auth/device/start", "", api.DeviceStartReq{Fingerprint: "fp-sync", Hostname: "box"})
	e.must(rec, http.StatusOK, "device start")
	rec, _ = e.do(http.MethodPost, "/v1/auth/device/activate", sessionToken, api.DeviceActivateReq{UserCode: start["user_code"].(string)})
	e.must(rec, http.StatusOK, "activate")
	rec, toks := e.do(http.MethodPost, "/v1/auth/device/poll", "", api.DevicePollReq{DeviceCode: start["device_code"].(string)})
	e.must(rec, http.StatusOK, "poll")
	return toks["access_token"].(string), toks["machine_id"].(string)
}

func (e *env) projectID(token, slug string) string {
	e.t.Helper()
	rec, list := e.do(http.MethodGet, "/v1/projects", token, nil)
	e.must(rec, http.StatusOK, "projects")
	for _, it := range list["items"].([]any) {
		if m := it.(map[string]any); m["slug"] == slug {
			return m["id"].(string)
		}
	}
	e.t.Fatalf("找不到项目 %s", slug)
	return ""
}

func TestIntegration_SnapshotBlobAndResults(t *testing.T) {
	e := newEnv(t)
	device, _ := e.deviceFor(e.member)
	billing := e.projectID(e.member, "billing")

	rec, b := e.do(http.MethodPost, "/v1/bindings", device, api.BindingReq{WorkspaceID: "ws", DisplayName: "billing", ProjectIDs: []string{billing}})
	e.must(rec, http.StatusCreated, "bind")
	bindingID := b["id"].(string)

	// 快照
	rec, _ = e.do(http.MethodGet, "/v1/bindings/"+bindingID+"/snapshot", device, nil)
	e.must(rec, http.StatusOK, "snapshot")
	var snap sync.Snapshot
	decode(t, rec.Body.Bytes(), &snap)
	etag := rec.Header().Get("ETag")
	if etag == "" || snap.Revision == "" || etag != `"`+snap.Revision+`"` {
		t.Fatalf("ETag 应等于 revision: %q %q", etag, snap.Revision)
	}
	byKey := map[string]resources.Entry{}
	for _, r := range snap.Resources {
		byKey[string(r.Kind)+"/"+r.Name] = r
	}
	// 项目级 rule 覆盖组织级同名 rule
	if r := byKey["rule/security-baseline"]; r.Level != resources.LevelProject || r.Namespace != "billing" {
		t.Fatalf("项目级应覆盖组织级: %+v", r)
	}
	// 项目级 tombstone 屏蔽组织级 doc
	if _, ok := byKey["doc/arch/overview.md"]; ok {
		t.Fatal("被 tombstone 屏蔽的 doc 不应出现")
	}
	// 权限组内容：member 被授权，应拿到且命名空间为组 key；组内容不随 org 级自动分发
	if r, ok := byKey["skill/corp-db-review"]; !ok || r.Namespace != "backend-pack" {
		t.Fatalf("权限组资源: %+v", r)
	}
	if len(snap.Grants) != 1 || snap.Grants[0].GroupKey != "backend-pack" {
		t.Fatalf("grants: %+v", snap.Grants)
	}
	// policy 合并：组织 enforced + 项目 enforced；项目可覆盖 recall
	if len(snap.Policy.EnforcedRules) != 2 || !snap.Policy.RecallEnabled || len(snap.Policy.MCPAllowedHosts) != 1 {
		t.Fatalf("policy 合并错误: %+v", snap.Policy)
	}
	if snap.Culture == nil {
		t.Fatal("应带 culture")
	}
	for _, k := range []string{"skill/corp-common", "skill/billing-deploy", "agent/code-reviewer", "hook/lint-on-edit", "mcp/corp-docs", "env/CORP_API_BASE", "claudemd/conventions", "claudemd/billing-context", "learning/port-conflict-2026-09-01-a1b2"} {
		if _, ok := byKey[k]; !ok {
			t.Errorf("缺少 %s", k)
		}
	}

	// 304
	rec, _ = e.do(http.MethodGet, "/v1/bindings/"+bindingID+"/snapshot", device, nil, "If-None-Match", etag)
	e.must(rec, http.StatusNotModified, "304")

	// blob：可见的能取，且内容哈希对得上；随机哈希 404；owner 会话令牌不能取别人的绑定快照
	sha := byKey["skill/corp-common"].Files[0].SHA256
	rec, _ = e.do(http.MethodGet, "/v1/blobs/"+sha, device, nil)
	e.must(rec, http.StatusOK, "blob")
	if resources.BlobSum(rec.Body.Bytes()) != sha {
		t.Fatal("blob 内容与哈希不符")
	}
	rec, _ = e.do(http.MethodGet, "/v1/blobs/sha256:"+repeat("0", 64), device, nil)
	e.must(rec, http.StatusNotFound, "unknown blob")
	rec, _ = e.do(http.MethodGet, "/v1/bindings/"+bindingID+"/snapshot", e.owner, nil)
	e.must(rec, http.StatusNotFound, "别人的绑定")

	// 渲染成 teamai 目录应能跑通，且所有 blob 可取
	files, err := teamaifmt.Render(snap, func(s string) ([]byte, error) {
		rec, _ := e.do(http.MethodGet, "/v1/blobs/"+s, device, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("blob %s: %d", s, rec.Code)
		}
		return rec.Body.Bytes(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"teamai.yaml", "skills/corp-common/SKILL.md", "skills/billing-deploy/SKILL.md", "rules/security-baseline.md", "agents/code-reviewer.yaml", "hooks/hooks.yaml", "hooks/scripts/lint.sh", "mcp/mcp.yaml", "env/env.yaml", "claudemd/common/conventions.md", "claudemd/billing/billing-context.md", "culture.md", "learnings/port-conflict-2026-09-01-a1b2.md"} {
		if _, ok := files[p]; !ok {
			t.Errorf("渲染缺少 %s", p)
		}
	}
	if string(files["rules/security-baseline.md"])[:len("# 计费")] != "# 计费" {
		t.Fatalf("rule 应是项目级那份: %q", files["rules/security-baseline.md"])
	}

	// 回执
	rec, res := e.do(http.MethodPost, "/v1/bindings/"+bindingID+"/sync-results", device, api.SyncResultsReq{
		AppliedRevision: snap.Revision,
		Results: []sync.Result{
			{Kind: resources.KindSkill, Name: "corp-common", Action: "installed"},
			{Kind: resources.KindRule, Name: "security-baseline", Action: "conflict_skipped", Detail: map[string]any{"path": "rules/security-baseline.md"}},
		},
	})
	e.must(rec, http.StatusOK, "sync-results")
	if res["accepted"].(float64) != 2 {
		t.Fatalf("accepted: %v", res)
	}
	rec, bb := e.do(http.MethodGet, "/v1/bindings/"+bindingID, device, nil)
	e.must(rec, http.StatusOK, "get binding")
	if bb["applied_revision"] != snap.Revision || bb["last_sync_at"] == nil {
		t.Fatalf("回执应推进 applied_revision: %v", bb)
	}
	rec, _ = e.do(http.MethodPost, "/v1/bindings/"+bindingID+"/sync-results", device, api.SyncResultsReq{
		Results: []sync.Result{{Kind: resources.KindSkill, Name: "x", Action: "exploded"}},
	})
	e.must(rec, http.StatusUnprocessableEntity, "非法 action")

	// owner 没有被授权 backend-pack，也不是 billing 成员之外的人：owner 是 admin，能看到全部
	ownerDev, _ := e.deviceFor(e.owner)
	rec, ob := e.do(http.MethodPost, "/v1/bindings", ownerDev, api.BindingReq{WorkspaceID: "ws-o", ProjectIDs: []string{e.projectID(e.owner, "website")}})
	e.must(rec, http.StatusCreated, "owner bind")
	rec, _ = e.do(http.MethodGet, "/v1/bindings/"+ob["id"].(string)+"/snapshot", ownerDev, nil)
	e.must(rec, http.StatusOK, "owner snapshot")
	var osnap sync.Snapshot
	decode(t, rec.Body.Bytes(), &osnap)
	okeys := map[string]bool{}
	for _, r := range osnap.Resources {
		okeys[string(r.Kind)+"/"+r.Name] = true
	}
	if okeys["skill/corp-db-review"] {
		t.Fatal("未被授权的权限组内容不应下发")
	}
	if okeys["skill/billing-deploy"] || !okeys["doc/arch/overview.md"] {
		t.Fatalf("website 绑定不应拿到 billing 的内容，且组织级 doc 应可见: %v", okeys)
	}

	// 撤销绑定后快照为空清单
	rec, _ = e.do(http.MethodDelete, "/v1/bindings/"+bindingID, device, nil)
	e.must(rec, http.StatusOK, "unbind")
	rec, _ = e.do(http.MethodGet, "/v1/bindings/"+bindingID+"/snapshot", device, nil)
	e.must(rec, http.StatusOK, "revoked snapshot")
	var rsnap sync.Snapshot
	decode(t, rec.Body.Bytes(), &rsnap)
	if len(rsnap.Resources) != 0 {
		t.Fatal("撤销的绑定应返回空清单")
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func decode(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("解析响应: %v\n%s", err, b)
	}
}
