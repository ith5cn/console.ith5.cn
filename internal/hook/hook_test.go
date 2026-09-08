package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var at = time.Date(2026, 9, 3, 10, 3, 0, 0, time.UTC)

// forbidden 是 L0 绝不上报的内容（PRD §6.3）。
// 这些字符串一旦出现在队列行里，就是隐私事故。
var forbidden = []string{
	"secret-token-abc", // 凭据
	"文件的完整内容在这里",       // 文件内容
	"--password",       // 完整命令行参数
	"AWS_SECRET",       // 环境变量
	"/Users/zhangsan",  // home 绝对路径
}

func TestExtract_NeverLeaksForbiddenContent(t *testing.T) {
	in := Input{
		SessionID:     "s1",
		HookEventName: "PostToolUse",
		CWD:           "/Users/zhangsan/work/web",
		ToolName:      "Bash",
		ToolInput: ToolInput{
			FilePath:  "/Users/zhangsan/work/web/src/a.ts",
			Command:   "AWS_SECRET=secret-token-abc curl --password hunter2 https://x",
			OldString: "旧的一行",
			NewString: "新的一行\n又一行\n文件的完整内容在这里",
		},
	}
	line := string(Marshal(Extract(in, "/Users/zhangsan/work/web", "github.com/acme/web", at, "e1")))
	for _, bad := range forbidden {
		if strings.Contains(line, bad) {
			t.Fatalf("队列行泄露了禁运内容 %q:\n%s", bad, line)
		}
	}
}

func TestExtract_WhitelistFields(t *testing.T) {
	exit := 0
	in := Input{
		SessionID: "s1", HookEventName: "PostToolUse", ToolName: "Edit",
		ToolInput: ToolInput{
			FilePath:  "/repo/src/a.ts",
			OldString: "a",
			NewString: "a\nb\nc",
		},
		ToolResponse: ToolResp{ExitCode: &exit},
	}
	ev := Extract(in, "/repo", "github.com/acme/web", at, "e1")
	if ev.Summary.Repo != "github.com/acme/web" {
		t.Fatalf("repo: %q", ev.Summary.Repo)
	}
	if ev.Summary.FilePath != "src/a.ts" {
		t.Fatalf("file_path 应相对 repo 根: %q", ev.Summary.FilePath)
	}
	if ev.Summary.LinesChanged != 2 {
		t.Fatalf("lines_changed: %d", ev.Summary.LinesChanged)
	}
	if ev.EventType != "tool_use" {
		t.Fatalf("event_type: %q", ev.EventType)
	}
}

// 拿不到 repo 根时只留 basename —— 绝不让 home 绝对路径进队列。
func TestExtract_NoRepoFallsBackToBasename(t *testing.T) {
	in := Input{ToolInput: ToolInput{FilePath: "/Users/zhangsan/notes/a.md"}}
	ev := Extract(in, "", "", at, "e1")
	if ev.Summary.FilePath != "a.md" {
		t.Fatalf("应只留 basename: %q", ev.Summary.FilePath)
	}
}

// repo 之外的文件同样只留 basename。
func TestExtract_OutsideRepoFallsBackToBasename(t *testing.T) {
	in := Input{ToolInput: ToolInput{FilePath: "/Users/zhangsan/other/x.md"}}
	ev := Extract(in, "/Users/zhangsan/work/web", "github.com/acme/web", at, "e1")
	if ev.Summary.FilePath != "x.md" {
		t.Fatalf("应只留 basename: %q", ev.Summary.FilePath)
	}
}

func TestCommandName(t *testing.T) {
	cases := map[string]string{
		"npm run build":                     "npm",
		"/usr/local/bin/pnpm install":       "pnpm",
		"FOO=1 BAR=2 git push --force":      "git",  // 前置环境变量赋值要跳过
		"curl -H 'Authorization: Bearer x'": "curl", // 参数一律丢弃
		`"my cmd" arg`:                      "my",
		"":                                  "",
	}
	for in, want := range cases {
		if got := commandName(in); got != want {
			t.Errorf("%q → %q，期望 %q", in, got, want)
		}
	}
}

func TestNormalizeRemote(t *testing.T) {
	cases := map[string]string{
		"git@github.com:acme/web.git":           "github.com/acme/web",
		"https://github.com/acme/web.git":       "github.com/acme/web",
		"https://user:pass@gitlab.com/a/b.git":  "gitlab.com/a/b", // 凭据必须剥掉
		"https://GitHub.com/Acme/Web":           "github.com/Acme/Web",
		"ssh://git@git.corp.cn:2222/team/x.git": "git.corp.cn:2222/team/x",
		"":                                      "",
	}
	for in, want := range cases {
		if got := NormalizeRemote(in); got != want {
			t.Errorf("%q → %q，期望 %q", in, got, want)
		}
	}
}

func TestFindRepo(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	os.MkdirAll(gitDir, 0o755)
	os.WriteFile(filepath.Join(gitDir, "config"), []byte(
		"[core]\n\tbare = false\n[remote \"upstream\"]\n\turl = git@github.com:other/x.git\n"+
			"[remote \"origin\"]\n\turl = git@github.com:acme/web.git\n"), 0o644)
	deep := filepath.Join(root, "a", "b", "c")
	os.MkdirAll(deep, 0o755)

	gotRoot, gotRemote := FindRepo(deep)
	if gotRoot != root {
		t.Fatalf("root: %q", gotRoot)
	}
	if gotRemote != "github.com/acme/web" {
		t.Fatalf("应取 origin 而非 upstream: %q", gotRemote)
	}
}

func TestFindRepo_NotARepo(t *testing.T) {
	root, remote := FindRepo(t.TempDir())
	if root != "" || remote != "" {
		t.Fatalf("非 git 目录应返回空: %q %q", root, remote)
	}
}

// 超长事件降级为最小元数据，而不是写出一行坏 JSON 或撑爆队列。
func TestMarshal_OversizedDegrades(t *testing.T) {
	ev := Event{
		ID: "e1", SessionID: "s1", EventType: "tool_use", ToolName: "Bash",
		OccurredAt: at.Format(time.RFC3339Nano),
		Summary:    Summary{FilePath: strings.Repeat("x", maxEventBytes*2)},
	}
	line := Marshal(ev)
	if len(line) > maxEventBytes {
		t.Fatalf("超限事件未降级，长度 %d", len(line))
	}
	var back Event
	if err := json.Unmarshal(line[:len(line)-1], &back); err != nil {
		t.Fatalf("降级后仍须是合法 JSON: %v", err)
	}
	if back.ID != "e1" || back.ToolName != "Bash" {
		t.Fatal("降级后应保留最小元数据")
	}
}

// 单条事件必须小于 PIPE_BUF，否则多个 hook 进程并发 append 会交错。
func TestMarshal_FitsInPipeBuf(t *testing.T) {
	const pipeBuf = 4096
	if maxEventBytes >= pipeBuf {
		t.Fatalf("maxEventBytes(%d) 必须小于 PIPE_BUF(%d)，否则并发写会交错",
			maxEventBytes, pipeBuf)
	}
}

func TestAppend_Concurrent(t *testing.T) {
	q := filepath.Join(t.TempDir(), "queue", "events.ndjson")
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 20; j++ {
				_ = Append(q, Marshal(Event{ID: "x", EventType: "tool_use", ToolName: "Edit"}))
			}
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
	b, err := os.ReadFile(q)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("并发写产生了坏行: %q", line)
		}
		n++
	}
	if n != 400 {
		t.Fatalf("应有 400 行，实际 %d", n)
	}
}
