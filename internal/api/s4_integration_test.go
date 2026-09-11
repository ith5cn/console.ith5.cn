package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/ith5/ith5/internal/api"
	"github.com/ith5/ith5/internal/resources"
	"github.com/ith5/ith5/internal/telemetry"
)

func TestIntegration_LearningsContributeArchivePromote(t *testing.T) {
	e := newEnv(t)
	billing := e.projectID(e.member, "billing")

	// 含密钥的内容被拒，且错误码稳定
	rec, body := e.do(http.MethodPost, "/v1/learnings", e.member, api.ContributeReq{
		Title: "泄露", Content: "用这个 key: AKIAABCDEFGHIJKLMNOP 就行", ProjectID: billing,
	})
	e.must(rec, http.StatusUnprocessableEntity, "secret")
	if errCodeOf(body) != "SECRET_DETECTED" {
		t.Fatalf("错误码: %v", body)
	}

	// member 直接发布一条项目级经验（组织策略默认不审核）
	rec, cs := e.do(http.MethodPost, "/v1/learnings", e.member, api.ContributeReq{
		Title: "迁移预演要用只读账号", Content: "## 现象\n\n预演时误写了生产库。\n\n## 做法\n\n预演一律用只读账号。", ProjectID: billing, Tags: []string{"db", "deploy"},
	})
	e.must(rec, http.StatusCreated, "contribute")
	if cs["state"] != "published" {
		t.Fatalf("learning 应直接发布: %v", cs["state"])
	}

	// 知识库能检索到（全文与关键字），且带置信度与摘要；未绑定项目的 owner 是 admin 也能看
	rec, list := e.do(http.MethodGet, "/v1/learnings?q=只读账号&project_id="+billing, e.member, nil)
	e.must(rec, http.StatusOK, "search")
	items := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("应检索到 1 条: %v", list)
	}
	l := items[0].(map[string]any)
	learningID := l["id"].(string)
	if l["title"] != "迁移预演要用只读账号" || l["namespace"] != "billing" || l["excerpt"] == "" || l["author"] == "" {
		t.Fatalf("learning 字段: %v", l)
	}
	rec, one := e.do(http.MethodGet, "/v1/learnings/"+learningID, e.owner, nil)
	e.must(rec, http.StatusOK, "get learning")
	if one["content"] == nil || one["content"].(string) == "" {
		t.Fatal("详情应带正文")
	}

	// 设备令牌上报召回与点赞 → 置信度变化 → kb-health 里出现在高频召回
	device, _ := e.deviceFor(e.member)
	now := time.Now().UTC().Format(time.RFC3339)
	rec, ack := e.do(http.MethodPost, "/v1/reports/events", device, api.ReportsReq{Events: []telemetry.Event{
		{EventID: "ev-1", Seq: 1, Type: telemetry.TypeVoteDelta, OccurredAt: time.Now(), Payload: map[string]any{"bundle_id": learningID, "recalled_delta": 6.0, "upvoted_delta": 5.0}},
		{EventID: "ev-2", Seq: 2, Type: telemetry.TypeSkillUsage, OccurredAt: time.Now(), Payload: map[string]any{"skill": "corp-common", "count": 3.0}},
		{EventID: "ev-3", Seq: 3, Type: telemetry.TypeSessionSummary, OccurredAt: time.Now(), Payload: map[string]any{
			"session_id": "s-1", "tool": "claude", "started_at": now, "duration_ms": 90000.0, "prompt_turns": 4.0, "tool_total": 12.0,
			"tool_sequence": []any{"Read", "Edit"}, "interventions": map[string]any{"interrupt": 1.0}, "valuable": true,
			"promptSummary": "fix prod", "transcriptPath": "/Users/x/t.jsonl",
		}},
		{EventID: "ev-4", Seq: 4, Type: telemetry.TypeUsageDaily, OccurredAt: time.Now(), Payload: map[string]any{
			"day": time.Now().UTC().Format("2006-01-02"), "sessions_ended": 2.0, "sessions_succeeded": 1.0, "prompt_turns": 9.0, "duration_ms": 120000.0, "cost_micros": 5000.0,
		}},
		{EventID: "ev-5", Seq: 5, Type: telemetry.TypeToolUse, OccurredAt: time.Now(), Payload: map[string]any{
			"session_id": "s-1", "event_type": "tool_use", "tool_name": "Edit", "repo": "billing", "file_path": "x.go", "lines_changed": 3.0,
		}},
	}})
	e.must(rec, http.StatusOK, "report")
	if len(ack["accepted"].([]any)) != 5 || ack["max_seq"].(float64) != 5 {
		t.Fatalf("ack: %v", ack)
	}
	// 重传同一批：全部去重，不报错
	rec, ack = e.do(http.MethodPost, "/v1/reports/events", device, api.ReportsReq{Events: []telemetry.Event{
		{EventID: "ev-1", Seq: 1, Type: telemetry.TypeVoteDelta, OccurredAt: time.Now(), Payload: map[string]any{"bundle_id": learningID, "recalled_delta": 6.0, "upvoted_delta": 5.0}},
	}})
	e.must(rec, http.StatusOK, "report again")
	if len(ack["accepted"].([]any)) != 0 {
		t.Fatalf("重复事件不应再次接受: %v", ack)
	}
	// 会话令牌不能上报
	rec, _ = e.do(http.MethodPost, "/v1/reports/events", e.member, api.ReportsReq{})
	e.must(rec, http.StatusForbidden, "session token report")

	rec, one = e.do(http.MethodGet, "/v1/learnings/"+learningID, e.member, nil)
	e.must(rec, http.StatusOK, "get after votes")
	if one["recalled"].(float64) != 6 || one["upvoted"].(float64) != 5 || one["confidence"].(float64) < 0.7 {
		t.Fatalf("投票应累计且置信度升高: %v", one)
	}

	rec, health := e.do(http.MethodGet, "/v1/reports/kb-health", e.owner, nil)
	e.must(rec, http.StatusOK, "kb-health")
	if health["by_kind"].(map[string]any)["learning"].(float64) < 2 {
		t.Fatalf("by_kind: %v", health["by_kind"])
	}
	if top := health["top_recalled"].([]any); len(top) != 1 || top[0].(map[string]any)["id"] != learningID {
		t.Fatalf("top_recalled: %v", health["top_recalled"])
	}
	if cands := health["promote_candidates"].([]any); len(cands) != 1 {
		t.Fatalf("置信度高且召回 ≥5 应进晋升候选: %v", health["promote_candidates"])
	}
	if silent := health["silent"].([]any); len(silent) != 1 {
		t.Fatalf("seed 里那条从未召回的应在 silent: %v", health["silent"])
	}

	// 周报与用量：seed 之外只有刚才那一条
	rec, digest := e.do(http.MethodGet, "/v1/reports/digest", e.owner, nil)
	e.must(rec, http.StatusOK, "digest")
	if digest["sessions"].(float64) != 2 || digest["cost_micros"].(float64) != 5000 || digest["active_members"].(float64) != 1 {
		t.Fatalf("digest: %v", digest)
	}
	if hl := digest["highlights"].([]any); len(hl) != 1 || hl[0].(map[string]any)["tool_total"].(float64) != 12 {
		t.Fatalf("highlights: %v", digest["highlights"])
	}
	if ts := digest["top_skills"].([]any); len(ts) != 1 || ts[0].(map[string]any)["count"].(float64) != 3 {
		t.Fatalf("top_skills: %v", digest["top_skills"])
	}
	rec, usage := e.do(http.MethodGet, "/v1/reports/usage", e.owner, nil)
	e.must(rec, http.StatusOK, "usage")
	if len(usage["items"].([]any)) != 1 {
		t.Fatalf("usage: %v", usage)
	}
	rec, _ = e.do(http.MethodGet, "/v1/reports/usage", e.member, nil)
	e.must(rec, http.StatusForbidden, "member usage")

	// 工具调用审计落到了 execution_events（通过旧审计接口可见）
	rec, ex := e.do(http.MethodGet, "/v1/audit/executions", e.owner, nil)
	if rec.Code == http.StatusOK && len(ex["items"].([]any)) == 0 {
		t.Fatal("tool_use 应写入审计")
	}

	// 晋升：生成 rule 草稿变更集，正文去掉 frontmatter
	rec, pcs := e.do(http.MethodPost, "/v1/learnings/"+learningID+"/promote", e.member, api.PromoteReq{Kind: "rule", Name: "readonly-rehearsal"})
	e.must(rec, http.StatusCreated, "promote")
	if pcs["state"] != "draft" || pcs["ops"].([]any)[0].(map[string]any)["kind"] != "rule" {
		t.Fatalf("晋升应生成 rule 草稿: %v", pcs)
	}

	// 归档：作者自己可以；归档后默认列表不再出现，status=archived 能看到
	rec, _ = e.do(http.MethodPost, "/v1/learnings/"+learningID+"/archive", e.member, nil)
	e.must(rec, http.StatusOK, "archive")
	rec, list = e.do(http.MethodGet, "/v1/learnings?project_id="+billing, e.member, nil)
	e.must(rec, http.StatusOK, "list after archive")
	for _, it := range list["items"].([]any) {
		if it.(map[string]any)["id"] == learningID {
			t.Fatal("归档后默认列表不应出现")
		}
	}
	rec, list = e.do(http.MethodGet, "/v1/learnings?project_id="+billing+"&status=archived", e.member, nil)
	e.must(rec, http.StatusOK, "list archived")
	if len(list["items"].([]any)) != 1 {
		t.Fatalf("archived 视图应有 1 条: %v", list)
	}
	rec, _ = e.do(http.MethodPost, "/v1/learnings/"+learningID+"/archive", e.member, nil)
	e.must(rec, http.StatusUnprocessableEntity, "重复归档")

	// 组织级经验：非项目成员（owner）也能分享到共享区；member 不能对不属于的项目分享
	rec, _ = e.do(http.MethodPost, "/v1/learnings", e.member, api.ContributeReq{Title: "x", Content: "y", ProjectID: e.projectID(e.owner, "website")})
	e.must(rec, http.StatusForbidden, "非成员分享到项目")
	rec, cs = e.do(http.MethodPost, "/v1/learnings", e.member, api.ContributeReq{Title: "共享经验", Content: "全员可见"})
	e.must(rec, http.StatusCreated, "shared contribute")
	if cs["ops"].([]any)[0].(map[string]any)["level"] != "org" {
		t.Fatalf("应为 org 级: %v", cs)
	}
	_ = resources.KindLearning
}

func TestIntegration_LearningsReviewPolicy(t *testing.T) {
	e := newEnv(t)
	rec, me := e.do(http.MethodGet, "/v1/me", e.owner, nil)
	e.must(rec, http.StatusOK, "me")
	orgID := me["membership"].(map[string]any)["org_id"].(string)
	rec, _ = e.do(http.MethodPut, "/v1/organizations/"+orgID+"/policy", e.owner,
		api.PolicyJSON{RequiredApprovals: 1, LearningsReview: true, ConfidencePrune: 0.15, ConfidencePromote: 0.7, RetentionMonths: 13})
	e.must(rec, http.StatusOK, "policy")
	rec, cs := e.do(http.MethodPost, "/v1/learnings", e.member, api.ContributeReq{Title: "需审核", Content: "内容"})
	e.must(rec, http.StatusCreated, "contribute under review policy")
	if cs["state"] != "in_review" {
		t.Fatalf("开启审核后应进入 in_review: %v", cs["state"])
	}
}
