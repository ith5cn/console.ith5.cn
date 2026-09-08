package hook_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// p95 < 50ms 是硬指标（技术方案 §10.5）。
//
// 它是当初判定 hook 必须用独立 Go 二进制、而非 Node CLI 子命令的理由：
// PostToolUse 每次工具调用都同步触发，一次会话可能几百次；Node 的冷启动
// 地板就有 25–45ms，累积起来开发者会明确感知到卡顿。
//
// 因此这个测试不是锦上添花，而是防止有人把 hook 改回慢实现的护栏。
const p95Budget = 50 * time.Millisecond

func TestHookColdStartLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过性能测试")
	}
	bin := buildHook(t)
	home := t.TempDir()
	writeSandbox(t, home)
	input := realisticInput(t, home)

	// 预热：让页缓存与动态链接器就绪，避免首次运行的离群值主导结果
	for i := 0; i < 5; i++ {
		runHook(t, bin, home, input)
	}

	const n = 100
	durations := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		durations = append(durations, runHook(t, bin, home, input))
	}
	report(t, "冷启动", durations)
}

// 队列积压时不能变慢：O_APPEND 是 O(1)，但若实现改成读-改-写就会退化。
func TestHookLatencyWithBacklog(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过性能测试")
	}
	bin := buildHook(t)
	home := t.TempDir()
	writeSandbox(t, home)
	input := realisticInput(t, home)

	// 先积压 500 条
	for i := 0; i < 500; i++ {
		runHook(t, bin, home, input)
	}
	q := filepath.Join(home, ".ith5", "queue", "events.ndjson")
	if fi, err := os.Stat(q); err != nil {
		t.Fatal(err)
	} else {
		t.Logf("积压队列大小: %.1f KB", float64(fi.Size())/1024)
	}

	const n = 100
	durations := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		durations = append(durations, runHook(t, bin, home, input))
	}
	report(t, "500 条积压", durations)
}

func report(t *testing.T, label string, d []time.Duration) {
	t.Helper()
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	p := func(q float64) time.Duration { return d[int(float64(len(d)-1)*q)] }
	t.Logf("%s  n=%d  p50=%v  p95=%v  p99=%v  max=%v",
		label, len(d), p(0.50).Round(time.Millisecond/10),
		p(0.95).Round(time.Millisecond/10), p(0.99).Round(time.Millisecond/10),
		d[len(d)-1].Round(time.Millisecond/10))
	if got := p(0.95); got > p95Budget {
		t.Fatalf("%s 的 p95 = %v，超出预算 %v", label, got, p95Budget)
	}
}

func buildHook(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ith5-hook")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/ith5/ith5/cmd/ith5-hook")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("构建 hook 失败: %v\n%s", err, out)
	}
	return bin
}

func writeSandbox(t *testing.T, home string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Join(home, ".ith5", "queue"), 0o700))
	must(t, os.WriteFile(filepath.Join(home, ".ith5", "config.json"),
		[]byte(`{"telemetry":{"enabled":true}}`), 0o644))
	git := filepath.Join(home, "repo", ".git")
	must(t, os.MkdirAll(filepath.Join(home, "repo", "src", "deep", "nested"), 0o755))
	must(t, os.MkdirAll(git, 0o755))
	must(t, os.WriteFile(filepath.Join(git, "config"),
		[]byte("[remote \"origin\"]\n\turl = git@github.com:acme/web.git\n"), 0o644))
}

// realisticInput 造一个真实体量的 Edit 事件：一个 400 行的文件。
func realisticInput(t *testing.T, home string) []byte {
	t.Helper()
	var body strings.Builder
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&body, "const line%d = %d;\n", i, i)
	}
	b, err := json.Marshal(map[string]any{
		"session_id":      "sess-perf",
		"hook_event_name": "PostToolUse",
		// 深路径：迫使 FindRepo 向上走几层
		"cwd":       filepath.Join(home, "repo", "src", "deep", "nested"),
		"tool_name": "Edit",
		"tool_input": map[string]any{
			"file_path":  filepath.Join(home, "repo", "src", "a.ts"),
			"old_string": body.String(),
			"new_string": body.String() + "const added = 1;\n",
		},
		"tool_response": map[string]any{"exit_code": 0},
	})
	must(t, err)
	return b
}

func runHook(t *testing.T, bin, home string, input []byte) time.Duration {
	t.Helper()
	cmd := exec.Command(bin, "post-tool-use")
	cmd.Env = append(os.Environ(), "HOME="+home)
	cmd.Stdin = strings.NewReader(string(input))
	start := time.Now()
	out, err := cmd.CombinedOutput()
	d := time.Since(start)
	if err != nil {
		t.Fatalf("hook 执行失败: %v\n%s", err, out)
	}
	if len(out) > 0 {
		t.Fatalf("hook 不应有输出: %s", out)
	}
	return d
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
