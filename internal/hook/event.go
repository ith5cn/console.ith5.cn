// Package hook 实现 Claude Code 的 hook 入口逻辑：
// 从 stdin 读事件、按白名单提取最小元数据、追加本地队列、立刻退出。
//
// 关键约束（技术方案 §10.4、§10.5）：
//   - 绝不发网络、绝不调 git 子进程、绝不扫密钥、绝不读完整 transcript
//   - 队列中不得出现 prompt、文件内容、diff、完整命令、环境变量或 home 绝对路径
//   - p95 < 50ms，含冷启动
package hook

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

// Input 只声明我们关心的字段。
//
// encoding/json 会忽略未声明的字段，因此即便 tool_input 里带着一整个
// 文件的内容，它也不会进入我们的内存模型，更不会写进队列。
type Input struct {
	SessionID     string    `json:"session_id"`
	HookEventName string    `json:"hook_event_name"`
	CWD           string    `json:"cwd"`
	ToolName      string    `json:"tool_name"`
	ToolUseID     string    `json:"tool_use_id"`
	ToolInput     ToolInput `json:"tool_input"`
	ToolResponse  ToolResp  `json:"tool_response"`
}

type ToolInput struct {
	FilePath string `json:"file_path"`
	Command  string `json:"command"`
	// OldString/NewString 只用于估算改动行数，绝不出队列
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
	Content   string `json:"content"`

	// 以下三个字段**只供本机 trace 流**（trace.go），绝不进 Summary。
	// 声明在这里是为了让 hook 只解一次 stdin —— 解两次会把 p95 顶上去。
	// TestExtract_NeverLeaksTraceFields 守着这条边界。
	SubagentType string `json:"subagent_type"`
	Skill        string `json:"skill"`
	Description  string `json:"description"`
}

type ToolResp struct {
	ExitCode *int `json:"exit_code"`
}

// Event 是写入队列的一行。字段集合即隐私白名单（技术方案 §6.3）。
type Event struct {
	ID         string  `json:"id"`
	SessionID  string  `json:"session_id"`
	EventType  string  `json:"event_type"`
	ToolName   string  `json:"tool_name,omitempty"`
	Summary    Summary `json:"summary"`
	OccurredAt string  `json:"occurred_at"`
}

// Summary 是白名单本身。**新增字段前请先确认它不属于 L0 禁运清单。**
type Summary struct {
	// Repo 是 git remote origin 的 host+path，如 github.com/acme/web；
	// 非 git 仓库为空。绝不含本地绝对路径。
	Repo string `json:"repo,omitempty"`
	// FilePath 相对 repo 根；拿不到 repo 根时只留 basename。
	FilePath string `json:"file_path,omitempty"`
	// BashCommand 只取命令名（argv[0]），不含任何参数
	// —— 参数里常有路径、URL 甚至凭据。
	BashCommand  string `json:"bash_command,omitempty"`
	LinesChanged int    `json:"lines_changed,omitempty"`
	ExitCode     *int   `json:"exit_code,omitempty"`
}

// maxEventBytes 是单条事件的上限。
//
// 队列由 hook 进程 O_APPEND 写、CLI 进程读取轮转，两者并发。
// 单次写入小于 PIPE_BUF（4096）时 append 是原子的，不会交错。
// 超限的事件降级为最小元数据，宁可少记也不要写出一行坏 JSON。
const maxEventBytes = 3500

// Extract 把一次 hook 调用转成一条队列事件。
// repoRoot/repoURL 由调用方提供（读 .git/config 得到，不调 git 子进程）。
func Extract(in Input, repoRoot, repoURL string, now time.Time, id string) Event {
	ev := Event{
		ID:         id,
		SessionID:  in.SessionID,
		ToolName:   in.ToolName,
		OccurredAt: now.UTC().Format(time.RFC3339Nano),
	}
	switch in.HookEventName {
	case "SessionStart":
		ev.EventType = "session_start"
	default:
		ev.EventType = "tool_use"
	}
	ev.Summary.Repo = repoURL

	if p := in.ToolInput.FilePath; p != "" {
		ev.Summary.FilePath = relativize(p, repoRoot)
	}
	if c := in.ToolInput.Command; c != "" {
		ev.Summary.BashCommand = commandName(c)
	}
	ev.Summary.LinesChanged = countChangedLines(in.ToolInput)
	ev.Summary.ExitCode = in.ToolResponse.ExitCode
	return ev
}

// relativize 把绝对路径转成相对 repo 根的路径；
// 拿不到 repo 根、或路径在 repo 之外时只保留 basename
// —— 绝不让 home 绝对路径进入队列。
func relativize(p, repoRoot string) string {
	if repoRoot == "" {
		return filepath.Base(p)
	}
	rel, err := filepath.Rel(repoRoot, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Base(p)
	}
	return filepath.ToSlash(rel)
}

// commandName 只取命令名，丢掉全部参数。
//
// 完整命令行常含路径、URL、token，属 L0 禁运。这里也要处理
// `FOO=bar cmd` 这类前置环境变量赋值 —— 环境变量本身同样禁运。
func commandName(cmd string) string {
	for _, f := range strings.Fields(cmd) {
		if strings.Contains(f, "=") && !strings.ContainsAny(f, "/\\") {
			continue // 前置环境变量赋值，跳过且不记录
		}
		return filepath.Base(strings.Trim(f, `"'`))
	}
	return ""
}

// countChangedLines 只算行数，正文一律丢弃。
func countChangedLines(ti ToolInput) int {
	switch {
	case ti.OldString != "" || ti.NewString != "":
		return abs(lines(ti.NewString) - lines(ti.OldString))
	case ti.Content != "":
		return lines(ti.Content)
	}
	return 0
}

func lines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Marshal 把事件编码成一行 NDJSON。超限时降级为最小元数据。
func Marshal(ev Event) []byte {
	b, err := json.Marshal(ev)
	if err != nil || len(b)+1 > maxEventBytes {
		minimal := Event{
			ID: ev.ID, SessionID: ev.SessionID, EventType: ev.EventType,
			ToolName: ev.ToolName, OccurredAt: ev.OccurredAt,
		}
		b, err = json.Marshal(minimal)
		if err != nil {
			return nil
		}
	}
	return append(b, '\n')
}
