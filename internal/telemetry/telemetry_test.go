package telemetry

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParse_DropsForbiddenFields(t *testing.T) {
	ev := Event{EventID: "e1", Seq: 1, Type: TypeSessionSummary, OccurredAt: time.Now(), Payload: map[string]any{
		"session_id": "s1", "tool": "claude", "duration_ms": 1200.0, "prompt_turns": 3.0, "tool_total": 7.0,
		"tool_sequence": []any{"Read", "Edit", 42, "Bash"},
		"interventions": map[string]any{"interrupt": 2.0, "tool_reject": 1.0},
		"tokens":        map[string]any{"input": 100.0, "output": 50.0},
		"valuable":      true,
		// 禁运字段：解析后不应存在于任何结构里
		"promptSummary":  "fix the billing bug in prod",
		"stoppedOutput":  "done",
		"transcriptPath": "/Users/x/.claude/transcript.jsonl",
	}}
	p, err := Parse(ev)
	if err != nil {
		t.Fatal(err)
	}
	if p.Session == nil || p.Session.SessionID != "s1" || p.Session.ToolTotal != 7 || len(p.Session.ToolSequence) != 3 {
		t.Fatalf("解析错误: %+v", p.Session)
	}
	if p.Session.Interventions["interrupt"] != 2 || p.Session.Tokens["input"] != 100 || !p.Session.Valuable {
		t.Fatalf("计数字段丢失: %+v", p.Session)
	}
	// 结构体里根本没有 prompt 相关字段；用 %+v 打印全量确认没有泄露
	s := strings.ToLower(fmt.Sprintf("%+v", p.Session))
	for _, bad := range []string{"billing bug", "transcript", "done"} {
		if strings.Contains(s, bad) {
			t.Fatalf("禁运字段泄露: %s", s)
		}
	}
}

func TestParse_Validation(t *testing.T) {
	now := time.Now()
	bad := []Event{
		{EventID: "", Type: TypeVoteDelta, OccurredAt: now, Payload: map[string]any{"bundle_id": "b"}},
		{EventID: "e", Type: TypeVoteDelta, Payload: map[string]any{"bundle_id": "b"}},
		{EventID: "e", Type: TypeVoteDelta, OccurredAt: now, Payload: map[string]any{}},
		{EventID: "e", Type: TypeVoteDelta, OccurredAt: now, Payload: map[string]any{"bundle_id": "b", "recalled_delta": -1.0}},
		{EventID: "e", Type: TypeUsageDaily, OccurredAt: now, Payload: map[string]any{"day": "2026/09/11"}},
		{EventID: "e", Type: TypeSkillUsage, OccurredAt: now, Payload: map[string]any{"skill": "x", "count": 0.0}},
		{EventID: "e", Type: TypeToolUse, OccurredAt: now, Payload: map[string]any{"session_id": "s", "event_type": "weird"}},
		{EventID: "e", Type: EventType("bogus"), OccurredAt: now},
	}
	for i, ev := range bad {
		if _, err := Parse(ev); err == nil {
			t.Fatalf("用例 %d 应被拒绝", i)
		}
	}
	good := Event{EventID: "e", Type: TypeUsageDaily, OccurredAt: now, Payload: map[string]any{"day": "2026-09-11", "sessions_ended": 3.0, "cost_micros": 1234.0}}
	p, err := Parse(good)
	if err != nil || p.Usage.SessionsEnded != 3 || p.Usage.CostMicros != 1234 {
		t.Fatalf("usage_daily: %v %+v", err, p.Usage)
	}
}

func TestIngest_WindowAndBatch(t *testing.T) {
	svc := NewService(nil)
	old := Event{EventID: "e", Type: TypeSkillUsage, OccurredAt: time.Now().Add(-DedupeWindow - time.Hour), Payload: map[string]any{"skill": "x", "count": 1.0}}
	if _, err := svc.Ingest(nil, Context{}, []Event{old}); err == nil || !strings.Contains(err.Error(), "窗口") {
		t.Fatalf("超窗事件应拒绝: %v", err)
	}
	if ack, err := svc.Ingest(nil, Context{}, nil); err != nil || len(ack.Accepted) != 0 {
		t.Fatal("空批次应直接返回")
	}
}
