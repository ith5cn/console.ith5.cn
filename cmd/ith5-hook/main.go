// ith5-hook 是 Claude Code 调用的 hook 入口。
//
// 它是独立的 Go 二进制，不是 ith5 CLI 的子命令 —— 因为 PostToolUse
// 每次工具调用都会同步触发，一次会话可能几百次。Node 的冷启动地板
// 就有 25–45ms，累积起来开发者会明确感知到卡顿；Go 是 3–5ms。
//
// 它只做三件事：读 stdin → 按白名单提取 → 追加队列 → exit 0。
// 绝不发网络、不调 git 子进程、不扫密钥、不读完整 transcript。
package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/ith5/ith5/internal/hook"
)

// 无论发生什么都以 0 退出：hook 失败绝不能打断开发者的工作流。
// 诊断信息写 stderr，Claude Code 会记进 debug 日志。
func main() {
	if err := run(); err != nil {
		os.Stderr.WriteString("ith5-hook: " + err.Error() + "\n")
	}
	os.Exit(0)
}

func run() error {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 8<<20))
	if err != nil {
		return err
	}
	var in hook.Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	// 配置只读一次。两个开关各读一次文件的话，PostToolUse 这条
	// 每次 Edit/Bash 都同步触发的路径上就凭空多一次 syscall ——
	// 这个二进制是 Go 写的就是为了省这种开销。
	tele, trace := readFlags(filepath.Join(home, ".ith5", "config.json"))

	// 两条流各自独立开关、互不影响：trace 只落本机给 `ith5 watch` 看，
	// 上报关闭时它照常工作；反之遥测开着也不强制产生 trace。
	traceErr := writeTrace(home, trace, in)

	// 本机关闭上报时不产生任何事件（技术方案 §10.4）
	if !tele {
		return traceErr
	}

	root, remote := hook.FindRepo(in.CWD)
	ev := hook.Extract(in, root, remote, nowFn(), uuid.NewString())
	line := hook.Marshal(ev)
	if err := hook.Append(filepath.Join(home, ".ith5", "queue", "events.ndjson"), line); err != nil {
		return err
	}
	return traceErr
}

// writeTrace 落一条本机 trace。
//
// 先判 TraceKind 再判开关：高频路径（Edit/Bash/Read）在这里就是一次
// 字符串比较后直接返回，连配置都不必看。
func writeTrace(home string, enabled bool, in hook.Input) error {
	if in.HookEventName != "SessionStart" && in.HookEventName != "SubagentStop" &&
		hook.TraceKind(in.ToolName) == "" {
		return nil
	}
	if !enabled {
		return nil
	}
	tr, ok := hook.ExtractTrace(in, nowFn(), uuid.NewString())
	if !ok {
		return nil
	}
	return hook.Append(hook.TracePath(home, in.SessionID), hook.MarshalTrace(tr))
}

// readFlags 一次性取出两个开关。读不到配置时两者皆假
// —— 未登录/未配置时既不采集也不 trace。
func readFlags(cfgPath string) (telemetry, trace bool) {
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		return false, false
	}
	var cfg struct {
		Telemetry struct {
			Enabled bool `json:"enabled"`
		} `json:"telemetry"`
		Trace struct {
			Enabled bool `json:"enabled"`
		} `json:"trace"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return false, false
	}
	return cfg.Telemetry.Enabled, cfg.Trace.Enabled
}
