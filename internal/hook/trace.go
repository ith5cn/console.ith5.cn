package hook

// trace 是与遥测队列**完全分离**的第二条流，只服务本机的 `ith5 watch` 看板。
//
// 为什么不复用 events.ndjson：
//   - 队列受 L0 禁运清单约束且要上传服务端；看板需要 subagent_type、skill 名、
//     task 描述这些一旦上传就越界的字段。
//   - 队列在 telemetry.enabled 为假时（含未登录）完全静默，而看板必须
//     无论登录与否都能用 —— 它是开发者自己看自己的本机工具。
//
// 因此 trace 的硬规矩恰好与队列相反、也更简单：**只写本机磁盘，永不上传**。
// flush/sync 一侧不得读取 trace 目录。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

// Trace 是 trace 流的一行。
//
// 与 Event 不同，这里没有白名单收窄 —— 数据不出本机。但仍不收 prompt 与
// 文件内容：看板用不上，徒增磁盘与解析成本。
type Trace struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	// ToolUseID 把 start 与 end 配成一对。Claude Code 在 Pre/PostToolUse
	// 里给同一次调用相同的 tool_use_id；缺失时看板退化为按 (kind,name) 就近配对。
	ToolUseID string `json:"tool_use_id,omitempty"`
	// Kind: agent | skill | tool | session
	Kind string `json:"kind"`
	// Stage: start | end。PostToolUse 只在工具**结束后**触发，
	// 光有它看板会全程空白到最后一刻才刷出一整条，所以 start 必须由 PreToolUse 供给。
	Stage string `json:"stage"`
	// Name 是 subagent_type / skill 名 / 工具名。
	Name string `json:"name"`
	// Detail 是人类可读的一句话（Task 的 description）。仅本地。
	Detail string `json:"detail,omitempty"`
	// CWD 让看板能按项目分组，并据此去找 specs/*/tasks.md。
	CWD        string `json:"cwd,omitempty"`
	OccurredAt string `json:"occurred_at"`
}

// maxTraceBytes 与队列同理：超限降级，绝不写出坏 JSON 把 tail 卡住。
const maxTraceBytes = 8000

// maxDetailRunes 截断 description。它由模型生成，长度不可控。
const maxDetailRunes = 200

// TraceKind 判断这次调用值不值得进 trace，并给出归类。
//
// 只认三类：派 subagent、调 skill、以及会话开始。其余工具（Edit/Bash/…）
// 不进 trace —— 看板要回答的是「工作流走到哪了」，几百条 Edit 只会淹没它。
func TraceKind(toolName string) string {
	switch toolName {
	case "Task", "Agent":
		return "agent"
	case "Skill":
		return "skill"
	default:
		return ""
	}
}

// ExtractTrace 把一次 hook 调用转成一条 trace；不该记录时返回 ok=false。
func ExtractTrace(in Input, now time.Time, id string) (Trace, bool) {
	tr := Trace{
		ID:         id,
		SessionID:  in.SessionID,
		ToolUseID:  in.ToolUseID,
		CWD:        in.CWD,
		OccurredAt: now.UTC().Format(time.RFC3339Nano),
	}

	switch in.HookEventName {
	case "SessionStart":
		tr.Kind, tr.Stage, tr.Name = "session", "start", "session"
		return tr, true
	case "PreToolUse", "PostToolUse":
		kind := TraceKind(in.ToolName)
		if kind == "" {
			return Trace{}, false
		}
		tr.Kind = kind
		tr.Stage = "start"
		if in.HookEventName == "PostToolUse" {
			tr.Stage = "end"
		}
		tr.Name = traceName(kind, in)
		tr.Detail = truncate(in.ToolInput.Description, maxDetailRunes)
		return tr, true
	case "SubagentStop":
		// 兜底：subagent 以非常规方式结束时 PostToolUse 可能不来，
		// 没有它看板上会留下一条永远转圈的 agent。
		tr.Kind, tr.Stage, tr.Name = "agent", "end", ""
		return tr, true
	}
	return Trace{}, false
}

func traceName(kind string, in Input) string {
	switch kind {
	case "agent":
		if n := in.ToolInput.SubagentType; n != "" {
			return n
		}
		return "agent"
	case "skill":
		if n := in.ToolInput.Skill; n != "" {
			return n
		}
		return "skill"
	}
	return in.ToolName
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// MarshalTrace 编码成一行 NDJSON。超限时丢掉 Detail（唯一不定长的字段）。
func MarshalTrace(tr Trace) []byte {
	b, err := json.Marshal(tr)
	if err != nil {
		return nil
	}
	if len(b)+1 > maxTraceBytes {
		tr.Detail = ""
		if b, err = json.Marshal(tr); err != nil {
			return nil
		}
	}
	return append(b, '\n')
}

// TracePath 是某个会话的 trace 文件。按 session 分文件，
// 这样看板打开一个会话不必扫全部历史，删除旧会话也只是删文件。
func TracePath(home, sessionID string) string {
	name := sanitizeSession(sessionID) + ".ndjson"
	return filepath.Join(home, ".ith5", "trace", name)
}

// TraceDir 是 trace 根目录。
func TraceDir(home string) string { return filepath.Join(home, ".ith5", "trace") }

// sanitizeSession 挡住 ../ 一类的路径穿越 —— session_id 来自外部输入。
func sanitizeSession(s string) string {
	if s == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}
