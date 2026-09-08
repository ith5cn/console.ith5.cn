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
	// 本机关闭上报时不产生任何事件（技术方案 §10.4）
	if !telemetryEnabled(filepath.Join(home, ".ith5", "config.json")) {
		return nil
	}

	root, remote := hook.FindRepo(in.CWD)
	ev := hook.Extract(in, root, remote, nowFn(), uuid.NewString())
	line := hook.Marshal(ev)
	return hook.Append(filepath.Join(home, ".ith5", "queue", "events.ndjson"), line)
}

func telemetryEnabled(cfgPath string) bool {
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		return false // 未登录/未配置时不采集
	}
	var cfg struct {
		Telemetry struct {
			Enabled bool `json:"enabled"`
		} `json:"telemetry"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return false
	}
	return cfg.Telemetry.Enabled
}
