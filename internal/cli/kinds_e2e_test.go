package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ith5/ith5/internal/cli/link"
	"github.com/ith5/ith5/internal/core"
)

// 这个用例走完整条落盘链路：Store.Write -> Materialize/Apply -> lock，
// 断言每个新 kind 落在 Claude Code 真正会读的位置。
//
// 放错目录的后果是**静默失效**：同步显示成功，功能就是不生效，
// 本地也看不出任何异常——所以这条契约必须有测试钉住。
func TestKinds_端到端落盘位置(t *testing.T) {
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
	st := NewStore(p.Store)
	strategies := NewStrategies("symlink", p)
	m := &Merger{Paths: p}
	lock := NewLock("srv")

	cases := []struct {
		kind    core.Kind
		name    string
		content string
		// want 是相对 claude_home 的落盘路径；合并形态留空，另行断言
		want string
	}{
		{core.KindSkill, "sk", "---\ndescription: d\n---\n技能正文\n", "skills/sk/SKILL.md"},
		{core.KindAgent, "ag", "---\nname: ag\ndescription: d\n---\n派发用\n", "agents/ag.md"},
		{core.KindHook, "hk", "#!/usr/bin/env node\nprocess.exit(0)\n", "hooks/hk.js"},
		{core.KindWorkflow, "wf", "export const meta = { name: 'wf' }\n", "workflows/wf.js"},
		{core.KindStandard, "std", "# 规范\n\n条目\n", "standards/std.md"},
		{core.KindSetting, "cfg", `{"model":"opus"}`, ""},
		{core.KindMCP, "mcpsrv", `{"mcpServers":{"tg":{"command":"bun"}}}`, ""},
	}

	for _, c := range cases {
		files := []core.File{{Path: c.kind.EntryFile(), Content: c.content}}
		if err := core.ValidateForPublish(c.kind, c.name, files); err != nil {
			t.Fatalf("%s/%s 发布校验失败: %v", c.kind, c.name, err)
		}
		sum, err := core.Checksum(files)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Write(c.kind, c.name, sum, files); err != nil {
			t.Fatalf("%s/%s 写 store: %v", c.kind, c.name, err)
		}
		dir := st.Dir(c.kind, c.name, sum)
		ref := core.MakeRef(c.kind, c.name)
		entry := core.LockEntry{
			BundleID: "b-" + c.name, Name: c.name, Kind: c.kind, Shape: c.kind.Shape(),
			Version: 1, Checksum: sum, Target: p.Target(c.kind, c.name), Store: dir,
		}

		if c.kind.Shape() == core.ShapeMerge {
			keys, err := m.Apply(c.kind, dir, core.LockEntry{})
			if err != nil {
				t.Fatalf("%s/%s 合并: %v", c.kind, c.name, err)
			}
			entry.MergeKeys = keys
		} else {
			if err := strategies.For(c.kind.Shape()).Materialize(
				dir, p.Target(c.kind, c.name),
				link.MarkerData{
					BundleID: "b-" + c.name, BundleName: c.name,
					Kind: string(c.kind), Version: 1, Checksum: sum,
				},
				p.StoreCtx(c.kind, c.name)); err != nil {
				t.Fatalf("%s/%s 物化: %v", c.kind, c.name, err)
			}
		}
		lock.Bundles[ref] = entry
	}

	// 文件形态与目录形态：内容必须能从 claude_home 下的目标路径读到
	for _, c := range cases {
		if c.want == "" {
			continue
		}
		got, err := os.ReadFile(filepath.Join(p.ClaudeHome, filepath.FromSlash(c.want)))
		if err != nil {
			t.Errorf("%s/%s 应落在 %s: %v", c.kind, c.name, c.want, err)
			continue
		}
		if string(got) != c.content {
			t.Errorf("%s 的内容不对: %q", c.want, got)
		}
	}

	// 合并形态：settings.json 与 ~/.claude.json 各自被正确写入
	settings := readJSONForTest(t, filepath.Join(p.ClaudeHome, "settings.json"))
	if settings["model"] != "opus" {
		t.Errorf("setting 应合并进 settings.json，实际 %v", settings)
	}
	userCfg := readJSONForTest(t, p.UserConfig)
	servers, _ := userCfg["mcpServers"].(map[string]any)
	if _, ok := servers["tg"]; !ok {
		t.Errorf("mcp 应合并进 ~/.claude.json 的 mcpServers，实际 %v", userCfg)
	}

	// lock 重建：文件系统上的东西必须能被重新认领，且 kind 不能认错
	rebuilt := NewLock("srv")
	n, err := rebuilt.Rebuild(p, strategies)
	if err != nil {
		t.Fatal(err)
	}
	// 合并形态没有独立入口，重建不出来（这是设计如此，不是缺陷）
	wantN := 0
	for _, c := range cases {
		if c.kind.Shape() != core.ShapeMerge {
			wantN++
		}
	}
	if n != wantN {
		t.Fatalf("应重建出 %d 条，实际 %d：%v", wantN, n, rebuilt.Bundles)
	}
	for _, c := range cases {
		if c.kind.Shape() == core.ShapeMerge {
			continue
		}
		ref := core.MakeRef(c.kind, c.name)
		e, ok := rebuilt.Bundles[ref]
		if !ok {
			t.Errorf("%s 没被重建出来：%v", ref, rebuilt.Bundles)
			continue
		}
		if e.Kind != c.kind {
			t.Errorf("%s 的 kind 认成了 %s", ref, e.Kind)
		}
	}
}

func readJSONForTest(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("解析 %s: %v", path, err)
	}
	return m
}
