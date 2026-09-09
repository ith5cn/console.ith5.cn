package core

import "testing"

// 这是新增多 kind 后最容易复发的一条：同一个名字既是 skill 又是 agent。
// 真实场景就是 ith5-code-reviewer —— 一个是操作手册（skill），
// 一个是 subagent 定义（agent），两者都要装，落在不同目录。
//
// 键若只用 name，两条会在 lock 与 ownership 里互相覆盖，症状是每次 sync
// 都在同一个入口上反复安装/卸载，而两个入口谁也稳定不下来。
func TestPlan_同名不同kind互不干扰(t *testing.T) {
	metas := []BundleMeta{
		{ID: "b1", Name: "dup", Kind: KindSkill, Version: 1, Checksum: "c1"},
		{ID: "b2", Name: "dup", Kind: KindAgent, Version: 1, Checksum: "c2"},
	}
	items := Plan(metas, map[Ref]LockEntry{}, map[Ref]Ownership{})

	if len(items) != 2 {
		t.Fatalf("同名不同 kind 应各占一项，实际 %d 项：%+v", len(items), items)
	}
	byRef := map[Ref]PlanItem{}
	for _, it := range items {
		byRef[it.Ref] = it
	}
	sk, ok := byRef["skill/dup"]
	if !ok || sk.Action != ActionInstall || sk.Shape != ShapeDir {
		t.Errorf("skill/dup 应 install 且为目录形态，实际 %+v", sk)
	}
	ag, ok := byRef["agent/dup"]
	if !ok || ag.Action != ActionInstall || ag.Shape != ShapeFile {
		t.Errorf("agent/dup 应 install 且为文件形态，实际 %+v", ag)
	}
}

// 一个装了、另一个没装时，不能因为重名而误判。
func TestPlan_同名不同kind各自认自己的lock(t *testing.T) {
	metas := []BundleMeta{
		{ID: "b1", Name: "dup", Kind: KindSkill, Version: 2, Checksum: "new"},
		{ID: "b2", Name: "dup", Kind: KindAgent, Version: 1, Checksum: "c2"},
	}
	lock := map[Ref]LockEntry{
		"skill/dup": {BundleID: "b1", Name: "dup", Kind: KindSkill, Shape: ShapeDir, Version: 1, Checksum: "old"},
	}
	own := map[Ref]Ownership{"skill/dup": OwnMine, "agent/dup": OwnAbsent}

	byRef := map[Ref]Action{}
	for _, it := range Plan(metas, lock, own) {
		byRef[it.Ref] = it.Action
	}
	if byRef["skill/dup"] != ActionUpdate {
		t.Errorf("skill/dup 已装旧版应 update，实际 %v", byRef["skill/dup"])
	}
	if byRef["agent/dup"] != ActionInstall {
		t.Errorf("agent/dup 未装应 install，实际 %v", byRef["agent/dup"])
	}
}

func TestRef_拆分(t *testing.T) {
	for _, c := range []struct {
		ref  Ref
		kind Kind
		name string
	}{
		{"skill/a", KindSkill, "a"},
		{"agent/x-y", KindAgent, "x-y"},
		{"mcp/telegram", KindMCP, "telegram"},
	} {
		if k, n := c.ref.Split(); k != c.kind || n != c.name {
			t.Errorf("%q 应拆成 (%s, %s)，实际 (%s, %s)", c.ref, c.kind, c.name, k, n)
		}
		if got := MakeRef(c.kind, c.name); got != c.ref {
			t.Errorf("MakeRef(%s, %s) 应为 %q，实际 %q", c.kind, c.name, c.ref, got)
		}
	}
}

// 落盘位置是新增 kind 的核心契约：放错目录 = Claude Code 根本不加载它，
// 而且完全静默——同步显示成功，功能就是不生效。
func TestKind_落盘契约(t *testing.T) {
	for _, c := range []struct {
		kind  Kind
		shape Shape
		root  string
		ext   string
		entry string
	}{
		{KindSkill, ShapeDir, "skills", "", SkillFile},
		{KindCommand, ShapeDir, "skills", "", SkillFile},
		{KindAgent, ShapeFile, "agents", ".md", AgentFile},
		{KindHook, ShapeFile, "hooks", ".js", HookFile},
		{KindWorkflow, ShapeFile, "workflows", ".js", WorkflowFile},
		{KindStandard, ShapeFile, "standards", ".md", StandardFile},
		{KindSetting, ShapeMerge, "", "", SettingFile},
		{KindMCP, ShapeMerge, "", "", MCPFile},
	} {
		if got := c.kind.Shape(); got != c.shape {
			t.Errorf("%s 的形态应为 %s，实际 %s", c.kind, c.shape, got)
		}
		if got := c.kind.Root(); got != c.root {
			t.Errorf("%s 的根目录应为 %q，实际 %q", c.kind, c.root, got)
		}
		if got := c.kind.Ext(); got != c.ext {
			t.Errorf("%s 的扩展名应为 %q，实际 %q", c.kind, c.ext, got)
		}
		if got := c.kind.EntryFile(); got != c.entry {
			t.Errorf("%s 的入口文件应为 %s，实际 %s", c.kind, c.entry, got)
		}
		if !c.kind.Valid() {
			t.Errorf("%s 应是合法 kind", c.kind)
		}
	}
}

// setting / mcp 的正文是 JSON，发布口必须挡住不合法的片段：
// 放过去的话，症状出现在员工机器上（合并失败），而管理员看到的是「发布成功」。
func TestValidateForPublish_JSON形态(t *testing.T) {
	setting := func(body string) []File { return []File{{Path: SettingFile, Content: body}} }
	mcp := func(body string) []File { return []File{{Path: MCPFile, Content: body}} }

	if err := ValidateForPublish(KindSetting, "s", setting(`{"env":{"A":"1"}}`)); err != nil {
		t.Errorf("合法 settings 片段应通过: %v", err)
	}
	if err := ValidateForPublish(KindSetting, "s", setting(`{"env":`)); err == nil {
		t.Error("语法错误的 JSON 必须被拒")
	}
	if err := ValidateForPublish(KindSetting, "s", setting(`[1,2]`)); err == nil {
		t.Error("顶层不是对象必须被拒——合并是按键进行的，数组没有键")
	}
	if err := ValidateForPublish(KindMCP, "m", mcp(`{"mcpServers":{"x":{"command":"npx"}}}`)); err != nil {
		t.Errorf("合法 MCP 片段应通过: %v", err)
	}
	if err := ValidateForPublish(KindMCP, "m", mcp(`{"servers":{}}`)); err == nil {
		t.Error("缺 mcpServers 必须被拒，否则装了等于没装")
	}
	if err := ValidateForPublish(KindMCP, "m", mcp(`{"mcpServers":{}}`)); err == nil {
		t.Error("空 mcpServers 必须被拒")
	}
}
