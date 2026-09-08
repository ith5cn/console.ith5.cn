package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readSettings(t *testing.T, dir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	return root
}

// ourGroup 取某事件下我方的那个 handler 组。
func ourGroup(t *testing.T, root map[string]any, event string) map[string]any {
	t.Helper()
	hooks, _ := root["hooks"].(map[string]any)
	groups, _ := hooks[event].([]any)
	for _, g := range groups {
		if groupIsOurs(g) {
			m, _ := g.(map[string]any)
			return m
		}
	}
	t.Fatalf("事件 %s 下没有我方 handler", event)
	return nil
}

func TestInstallHooks_RegistersAllEvents(t *testing.T) {
	dir := t.TempDir()
	changed, err := InstallHooks(dir, "/opt/ith5-hook")
	if err != nil || !changed {
		t.Fatalf("首次安装 changed=%v err=%v", changed, err)
	}
	root := readSettings(t, dir)
	for _, he := range hookEvents {
		g := ourGroup(t, root, he.Event)
		if m, _ := g["matcher"].(string); m != he.Matcher {
			t.Fatalf("%s 的 matcher = %q，期望 %q", he.Event, m, he.Matcher)
		}
	}
	// 看板依赖这两个：没有 PreToolUse 就看不到「正在跑」，
	// PostToolUse 不含 Task|Skill 就永远收不到结束。
	pre, _ := ourGroup(t, root, "PreToolUse")["matcher"].(string)
	for _, tool := range []string{"Task", "Agent", "Skill"} {
		if !strings.Contains(pre, tool) {
			t.Fatalf("PreToolUse matcher %q 漏了 %s —— 症状是看板永远空白且无报错", pre, tool)
		}
	}
}

func TestInstallHooks_Idempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := InstallHooks(dir, "/opt/ith5-hook"); err != nil {
		t.Fatal(err)
	}
	changed, err := InstallHooks(dir, "/opt/ith5-hook")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("重复安装不应报告变更")
	}
	hooks, _ := readSettings(t, dir)["hooks"].(map[string]any)
	groups, _ := hooks["PostToolUse"].([]any)
	if len(groups) != 1 {
		t.Fatalf("重复安装产生了 %d 个组", len(groups))
	}
}

// 升级路径：老机器上装的是旧 matcher。只更命令不更 matcher 的话，
// 新加的 Task|Skill 永远不触发，而且完全静默 —— 看板会一直空白。
func TestInstallHooks_UpgradesStaleMatcher(t *testing.T) {
	dir := t.TempDir()
	old := map[string]any{
		"hooks": map[string]any{
			"PostToolUse": []any{
				map[string]any{
					"matcher": "Edit|Write|MultiEdit|Bash",
					"hooks":   []any{map[string]any{"type": "command", "command": "/old/ith5-hook post-tool-use"}},
				},
			},
		},
	}
	b, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := InstallHooks(dir, "/new/ith5-hook")
	if err != nil || !changed {
		t.Fatalf("升级 changed=%v err=%v", changed, err)
	}
	g := ourGroup(t, readSettings(t, dir), "PostToolUse")
	m, _ := g["matcher"].(string)
	for _, tool := range []string{"Edit", "Bash", "Task", "Agent", "Skill"} {
		if !strings.Contains(m, tool) {
			t.Fatalf("matcher 未升级完整，%q 缺 %s", m, tool)
		}
	}
	inner, _ := g["hooks"].([]any)
	hm, _ := inner[0].(map[string]any)
	if c, _ := hm["command"].(string); c != "/new/ith5-hook post-tool-use" {
		t.Fatalf("命令未更新: %q", c)
	}
}

// 用户自己的 hook 绝不能被我们动到。
func TestInstallHooks_PreservesForeignHooks(t *testing.T) {
	dir := t.TempDir()
	mine := map[string]any{
		"hooks": map[string]any{
			"PostToolUse": []any{
				map[string]any{
					"matcher": "Write",
					"hooks":   []any{map[string]any{"type": "command", "command": "my-own-linter"}},
				},
			},
		},
	}
	b, _ := json.Marshal(mine)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallHooks(dir, "/opt/ith5-hook"); err != nil {
		t.Fatal(err)
	}
	hooks, _ := readSettings(t, dir)["hooks"].(map[string]any)
	groups, _ := hooks["PostToolUse"].([]any)
	if len(groups) != 2 {
		t.Fatalf("应保留用户的组并追加我们的，得到 %d 个", len(groups))
	}
	var foundForeign bool
	for _, g := range groups {
		m, _ := g.(map[string]any)
		inner, _ := m["hooks"].([]any)
		hm, _ := inner[0].(map[string]any)
		if c, _ := hm["command"].(string); c == "my-own-linter" {
			foundForeign = true
			if mm, _ := m["matcher"].(string); mm != "Write" {
				t.Fatalf("用户 hook 的 matcher 被改成了 %q", mm)
			}
		}
	}
	if !foundForeign {
		t.Fatal("用户自己的 hook 被弄丢了")
	}
}

func TestUninstallHooks_RemovesOnlyOurs(t *testing.T) {
	dir := t.TempDir()
	if _, err := InstallHooks(dir, "/opt/ith5-hook"); err != nil {
		t.Fatal(err)
	}
	if err := UninstallHooks(dir); err != nil {
		t.Fatal(err)
	}
	root := readSettings(t, dir)
	if _, ok := root["hooks"]; ok {
		t.Fatalf("我方是唯一 handler 时应把 hooks 整个删掉: %+v", root["hooks"])
	}
}
