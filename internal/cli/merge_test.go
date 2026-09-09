package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ith5/ith5/internal/core"
)

func mergeEnv(t *testing.T) (Paths, *Merger) {
	t.Helper()
	root := t.TempDir()
	p := Paths{
		Home:       filepath.Join(root, "ith5"),
		Store:      filepath.Join(root, "ith5", "store"),
		ClaudeHome: filepath.Join(root, "claude"),
		Staging:    filepath.Join(root, "claude", ".ith5-staging"),
		Trash:      filepath.Join(root, "claude", ".ith5-trash"),
		UserConfig: filepath.Join(root, ".claude.json"),
	}
	if err := p.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	return p, &Merger{Paths: p}
}

// storeFragment 在 store 里放一份片段，返回内容目录。
func storeFragment(t *testing.T, p Paths, kind core.Kind, name, sum, body string) string {
	t.Helper()
	dir := filepath.Join(p.Store, string(kind), name, sum)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, kind.EntryFile()), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readObj(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// 合并只动自己的键，用户原有配置必须原样保留。
func TestMerger_合并保留用户既有键(t *testing.T) {
	p, m := mergeEnv(t)
	settings := p.MergeFile(core.KindSetting)
	if err := os.WriteFile(settings, []byte(`{"model":"opus","theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := storeFragment(t, p, core.KindSetting, "corp", "aaaa", `{"env":{"A":"1"}}`)

	keys, err := m.Apply(core.KindSetting, dir, core.LockEntry{})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "env" {
		t.Fatalf("应只认领 env 一个键，实际 %v", keys)
	}
	got := readObj(t, settings)
	if got["model"] != "opus" || got["theme"] != "dark" {
		t.Fatalf("用户原有键被动了：%v", got)
	}
	if _, ok := got["env"]; !ok {
		t.Fatal("我方的键没写进去")
	}
	// 写入前必须留一份备份
	if _, err := os.Stat(settings + ".ith5.bak"); err != nil {
		t.Fatal("应备份原 settings.json")
	}
}

// 归属判定：值没被动过就是我方的。
func TestMerger_未被改动判为我方(t *testing.T) {
	p, m := mergeEnv(t)
	dir := storeFragment(t, p, core.KindSetting, "corp", "aaaa", `{"env":{"A":"1"}}`)
	keys, err := m.Apply(core.KindSetting, dir, core.LockEntry{})
	if err != nil {
		t.Fatal(err)
	}
	locked := core.LockEntry{Name: "corp", Kind: core.KindSetting, Store: dir, MergeKeys: keys}

	own, err := m.Inspect(core.KindSetting, locked)
	if err != nil {
		t.Fatal(err)
	}
	if own != core.OwnMine {
		t.Fatalf("未被改动应判为我方所有，实际 %v", own)
	}
}

// 这是整个合并形态最重要的一条闸门：用户改过的值，我们不再声称所有权，
// 因而既不会被覆盖，也不会被 Release 删掉。
func TestMerger_用户改过后不再认领也不删除(t *testing.T) {
	p, m := mergeEnv(t)
	settings := p.MergeFile(core.KindSetting)
	dir := storeFragment(t, p, core.KindSetting, "corp", "aaaa", `{"env":{"A":"1"}}`)
	keys, err := m.Apply(core.KindSetting, dir, core.LockEntry{})
	if err != nil {
		t.Fatal(err)
	}
	locked := core.LockEntry{Name: "corp", Kind: core.KindSetting, Store: dir, MergeKeys: keys}

	// 员工把值改成了自己的
	if err := os.WriteFile(settings, []byte(`{"env":{"A":"我改的"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	own, err := m.Inspect(core.KindSetting, locked)
	if err != nil {
		t.Fatal(err)
	}
	if own != core.OwnForeign {
		t.Fatalf("改动过应判为用户自有，实际 %v", own)
	}

	// 撤权也不能删掉他改过的值
	if err := m.Release(core.KindSetting, locked); err != nil {
		t.Fatal(err)
	}
	got := readObj(t, settings)
	env, _ := got["env"].(map[string]any)
	if env["A"] != "我改的" {
		t.Fatalf("用户改过的值被删了：%v", got)
	}
}

// 首装时目标键已存在：无从证明是自己写的，一律不碰。
func TestMerger_首装遇既有键判为冲突(t *testing.T) {
	p, m := mergeEnv(t)
	settings := p.MergeFile(core.KindSetting)
	if err := os.WriteFile(settings, []byte(`{"env":{"A":"用户自己配的"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// 没有 lock 记录，但我们知道这一版会写 env（由上一次的 MergeKeys 模拟）
	locked := core.LockEntry{MergeKeys: []string{"env"}}

	own, err := m.Inspect(core.KindSetting, locked)
	if err != nil {
		t.Fatal(err)
	}
	if own != core.OwnForeign {
		t.Fatalf("拿不出证据就不能认领，应判 foreign，实际 %v", own)
	}
}

// Release 只删我方写入且未被改动的键。
func TestMerger_Release只删自己的键(t *testing.T) {
	p, m := mergeEnv(t)
	settings := p.MergeFile(core.KindSetting)
	if err := os.WriteFile(settings, []byte(`{"model":"opus"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := storeFragment(t, p, core.KindSetting, "corp", "aaaa", `{"env":{"A":"1"}}`)
	keys, err := m.Apply(core.KindSetting, dir, core.LockEntry{})
	if err != nil {
		t.Fatal(err)
	}
	locked := core.LockEntry{Store: dir, MergeKeys: keys}

	if err := m.Release(core.KindSetting, locked); err != nil {
		t.Fatal(err)
	}
	got := readObj(t, settings)
	if _, ok := got["env"]; ok {
		t.Fatal("我方的键应被撤掉")
	}
	if got["model"] != "opus" {
		t.Fatalf("用户的键不该受影响：%v", got)
	}
}

// MCP 的管理单元是 mcpServers 下的每个 server，不是整个 mcpServers 对象。
// 否则两个 MCP bundle 会互相覆盖对方的 server。
func TestMerger_MCP按server粒度合并(t *testing.T) {
	p, m := mergeEnv(t)
	cfg := p.MergeFile(core.KindMCP)
	if cfg != p.UserConfig {
		t.Fatalf("MCP 应写进 ~/.claude.json，实际 %s", cfg)
	}
	if err := os.WriteFile(cfg, []byte(`{"mcpServers":{"userOwn":{"command":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	a := storeFragment(t, p, core.KindMCP, "a", "aaaa", `{"mcpServers":{"figma":{"command":"npx"}}}`)
	b := storeFragment(t, p, core.KindMCP, "b", "bbbb", `{"mcpServers":{"playwright":{"command":"npx"}}}`)

	keysA, err := m.Apply(core.KindMCP, a, core.LockEntry{})
	if err != nil {
		t.Fatal(err)
	}
	if len(keysA) != 1 || keysA[0] != "figma" {
		t.Fatalf("管理单元应是 server 名，实际 %v", keysA)
	}
	if _, err := m.Apply(core.KindMCP, b, core.LockEntry{}); err != nil {
		t.Fatal(err)
	}

	servers, _ := readObj(t, cfg)["mcpServers"].(map[string]any)
	for _, want := range []string{"userOwn", "figma", "playwright"} {
		if _, ok := servers[want]; !ok {
			t.Errorf("%s 应在 mcpServers 里，实际 %v", want, servers)
		}
	}

	// 撤掉 a，只应少 figma
	if err := m.Release(core.KindMCP, core.LockEntry{Store: a, MergeKeys: keysA}); err != nil {
		t.Fatal(err)
	}
	servers, _ = readObj(t, cfg)["mcpServers"].(map[string]any)
	if _, ok := servers["figma"]; ok {
		t.Error("figma 应被撤掉")
	}
	if _, ok := servers["playwright"]; !ok {
		t.Error("另一个 bundle 的 server 被误删了")
	}
	if _, ok := servers["userOwn"]; !ok {
		t.Error("用户自己的 server 被误删了")
	}
}

// 新版本不再包含的键要撤掉，否则内容删了配置还留着。
func TestMerger_升级撤掉上一版多出的键(t *testing.T) {
	p, m := mergeEnv(t)
	settings := p.MergeFile(core.KindSetting)
	v1 := storeFragment(t, p, core.KindSetting, "corp", "aaaa", `{"env":{"A":"1"},"model":"opus"}`)
	keys1, err := m.Apply(core.KindSetting, v1, core.LockEntry{})
	if err != nil {
		t.Fatal(err)
	}
	prev := core.LockEntry{Store: v1, MergeKeys: keys1}

	v2 := storeFragment(t, p, core.KindSetting, "corp", "bbbb", `{"env":{"A":"2"}}`)
	if _, err := m.Apply(core.KindSetting, v2, prev); err != nil {
		t.Fatal(err)
	}
	got := readObj(t, settings)
	if _, ok := got["model"]; ok {
		t.Fatalf("上一版写过、这一版不含的键应被撤掉：%v", got)
	}
	env, _ := got["env"].(map[string]any)
	if env["A"] != "2" {
		t.Fatalf("新版本的值没生效：%v", got)
	}
}

// settings.json 语法坏掉时必须报错，绝不能当成空对象写回去——
// 那等于把用户的全部配置抹掉。
func TestMerger_坏掉的settings必须报错(t *testing.T) {
	p, m := mergeEnv(t)
	settings := p.MergeFile(core.KindSetting)
	if err := os.WriteFile(settings, []byte(`{"env":`), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := storeFragment(t, p, core.KindSetting, "corp", "aaaa", `{"env":{"A":"1"}}`)

	if _, err := m.Apply(core.KindSetting, dir, core.LockEntry{}); err == nil {
		t.Fatal("解析失败必须报错，不能覆盖写回")
	}
	b, _ := os.ReadFile(settings)
	if string(b) != `{"env":` {
		t.Fatalf("原文件不该被改动，实际 %q", b)
	}
}
