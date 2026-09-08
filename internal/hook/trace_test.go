package hook

import (
	"encoding/json"
	"strings"
	"testing"
)

// trace 字段是为本机看板加的，它们**绝不能**出现在要上传的队列行里。
// 这条边界靠这个测试守着 —— 一旦有人把 Description 塞进 Summary，这里就红。
func TestExtract_NeverLeaksTraceFields(t *testing.T) {
	in := Input{
		SessionID:     "s1",
		HookEventName: "PostToolUse",
		ToolName:      "Task",
		ToolUseID:     "toolu_abc",
		ToolInput: ToolInput{
			SubagentType: "ith5-backend-engineer",
			Skill:        "ith5-ai",
			Description:  "实现登录接口并接入 better-auth",
		},
	}
	line := string(Marshal(Extract(in, "/repo", "github.com/acme/web", at, "e1")))
	for _, bad := range []string{"ith5-backend-engineer", "ith5-ai", "实现登录接口", "toolu_abc"} {
		if strings.Contains(line, bad) {
			t.Fatalf("遥测队列行泄露了 trace 专属字段 %q:\n%s", bad, line)
		}
	}
}

func TestExtractTrace_AgentStartAndEnd(t *testing.T) {
	base := Input{
		SessionID: "s1", CWD: "/repo", ToolName: "Task", ToolUseID: "toolu_1",
		ToolInput: ToolInput{SubagentType: "ith5-frontend-engineer", Description: "写登录页"},
	}

	base.HookEventName = "PreToolUse"
	start, ok := ExtractTrace(base, at, "t1")
	if !ok {
		t.Fatal("PreToolUse 应产生 trace")
	}
	if start.Kind != "agent" || start.Stage != "start" {
		t.Fatalf("kind/stage = %q/%q", start.Kind, start.Stage)
	}
	if start.Name != "ith5-frontend-engineer" {
		t.Fatalf("name = %q", start.Name)
	}
	if start.Detail != "写登录页" {
		t.Fatalf("detail = %q", start.Detail)
	}

	base.HookEventName = "PostToolUse"
	end, ok := ExtractTrace(base, at, "t2")
	if !ok || end.Stage != "end" {
		t.Fatalf("PostToolUse 应产生 end，得到 ok=%v stage=%q", ok, end.Stage)
	}
	if end.ToolUseID != start.ToolUseID {
		t.Fatal("start 与 end 的 tool_use_id 必须一致，否则配不上对")
	}
}

func TestExtractTrace_SkillAndSession(t *testing.T) {
	sk, ok := ExtractTrace(Input{
		SessionID: "s1", HookEventName: "PreToolUse", ToolName: "Skill",
		ToolInput: ToolInput{Skill: "ith5-code-reviewer"},
	}, at, "t1")
	if !ok || sk.Kind != "skill" || sk.Name != "ith5-code-reviewer" {
		t.Fatalf("skill trace = %+v ok=%v", sk, ok)
	}

	ss, ok := ExtractTrace(Input{SessionID: "s1", HookEventName: "SessionStart"}, at, "t2")
	if !ok || ss.Kind != "session" {
		t.Fatalf("session trace = %+v ok=%v", ss, ok)
	}
}

// 高频工具不进 trace：看板要回答「工作流走到哪」，
// 几百条 Edit/Bash 只会把它淹掉。
func TestExtractTrace_IgnoresHighFrequencyTools(t *testing.T) {
	for _, tool := range []string{"Edit", "Write", "Bash", "Read", "Grep"} {
		if _, ok := ExtractTrace(Input{
			SessionID: "s1", HookEventName: "PostToolUse", ToolName: tool,
		}, at, "t1"); ok {
			t.Fatalf("%s 不应进 trace", tool)
		}
	}
}

func TestExtractTrace_SubagentStopClosesSpan(t *testing.T) {
	tr, ok := ExtractTrace(Input{SessionID: "s1", HookEventName: "SubagentStop"}, at, "t1")
	if !ok || tr.Kind != "agent" || tr.Stage != "end" {
		t.Fatalf("SubagentStop 应产生 agent/end，得到 %+v ok=%v", tr, ok)
	}
}

func TestMarshalTrace_DropsDetailWhenOversized(t *testing.T) {
	tr := Trace{ID: "t1", SessionID: "s1", Kind: "agent", Stage: "start", Name: "x"}
	tr.Detail = strings.Repeat("很长的描述", 4000)
	line := MarshalTrace(tr)
	if len(line) > maxTraceBytes {
		t.Fatalf("超限行未降级，长度 %d", len(line))
	}
	var got Trace
	if err := json.Unmarshal(line, &got); err != nil {
		t.Fatalf("降级后仍须是合法 JSON: %v", err)
	}
	if got.Name != "x" {
		t.Fatalf("降级丢了关键字段: %+v", got)
	}
}

// session_id 来自外部输入，不能让它走出 trace 目录。
func TestTracePath_RejectsTraversal(t *testing.T) {
	p := TracePath("/home/u", "../../etc/passwd")
	if strings.Contains(p, "..") {
		t.Fatalf("路径穿越未被挡住: %s", p)
	}
	if !strings.HasPrefix(p, "/home/u/.ith5/trace/") {
		t.Fatalf("trace 文件跑出了 trace 目录: %s", p)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("  abc  ", 10); got != "abc" {
		t.Fatalf("应去掉首尾空白: %q", got)
	}
	// 按 rune 截断，不能把多字节字符切成半个
	got := truncate(strings.Repeat("中", 10), 3)
	if got != "中中中…" {
		t.Fatalf("按 rune 截断失败: %q", got)
	}
}
