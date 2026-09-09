package core

import (
	"errors"
	"strings"
	"testing"
)

func agentFiles(content string) []File {
	return []File{{Path: AgentFile, Content: content}}
}

func TestKind_形态映射(t *testing.T) {
	cases := []struct {
		kind  Kind
		shape Shape
		entry string
	}{
		{KindSkill, ShapeDir, SkillFile},
		{KindCommand, ShapeDir, SkillFile},
		{KindAgent, ShapeFile, AgentFile},
	}
	for _, c := range cases {
		if got := c.kind.Shape(); got != c.shape {
			t.Errorf("%s 的形态应为 %s，实际 %s", c.kind, c.shape, got)
		}
		if got := c.kind.EntryFile(); got != c.entry {
			t.Errorf("%s 的入口文件应为 %s，实际 %s", c.kind, c.entry, got)
		}
	}
}

func TestValidateFiles_agent只能一个文件(t *testing.T) {
	ok := agentFiles("---\nname: a\ndescription: d\n---\n")
	if err := ValidateFiles(KindAgent, ok); err != nil {
		t.Fatalf("合法的 agent 被拒: %v", err)
	}

	// 多带一个支持文件：文件形态的目标是一个 .md，没有目录能承载它，
	// 放行只会造成「发布成功、内容却下不去」。
	extra := append(agentFiles("---\nname: a\ndescription: d\n---\n"),
		File{Path: "references/x.md", Content: "x"})
	if err := ValidateFiles(KindAgent, extra); !errors.Is(err, ErrNoAgentMD) {
		t.Fatalf("带支持文件的 agent 必须被拒，实际 %v", err)
	}

	// 文件名不对
	wrongName := []File{{Path: "SKILL.md", Content: "---\nname: a\ndescription: d\n---\n"}}
	if err := ValidateFiles(KindAgent, wrongName); !errors.Is(err, ErrNoAgentMD) {
		t.Fatalf("入口必须叫 AGENT.md，实际 %v", err)
	}

	// 反向：skill 仍然可以多文件
	multi := []File{
		{Path: SkillFile, Content: "---\ndescription: d\n---\n"},
		{Path: "references/x.md", Content: "x"},
	}
	if err := ValidateFiles(KindSkill, multi); err != nil {
		t.Fatalf("skill 应允许支持文件: %v", err)
	}
}

func TestValidateFiles_未知kind被拒(t *testing.T) {
	if err := ValidateFiles(Kind("plugin"), agentFiles("x")); !errors.Is(err, ErrBadKind) {
		t.Fatalf("未知 kind 必须被拒，实际 %v", err)
	}
}

// 这是本次改动里最重要的一条闸门：frontmatter 的 name 与 bundle 名不一致时，
// agent 装得上但永远派发不到，且客户端**没有任何症状**——不报错、不缺文件、
// doctor 也查不出来。只能挡在发布口。
func TestValidateForPublish_agent名字必须对齐(t *testing.T) {
	good := agentFiles("---\nname: corp-backend\ndescription: 后端\n---\n正文\n")
	if err := ValidateForPublish(KindAgent, "corp-backend", good); err != nil {
		t.Fatalf("对齐的 agent 被拒: %v", err)
	}

	mismatch := agentFiles("---\nname: backend\ndescription: 后端\n---\n正文\n")
	err := ValidateForPublish(KindAgent, "corp-backend", mismatch)
	if !errors.Is(err, ErrAgentNameMismatch) {
		t.Fatalf("name 不一致必须被拒，实际 %v", err)
	}
	// 错误信息要说清后果，否则管理员会以为这是个可以忽略的洁癖校验
	if !strings.Contains(err.Error(), "派发不到") {
		t.Errorf("错误信息应说明后果，实际 %q", err)
	}

	missing := agentFiles("---\ndescription: 后端\n---\n正文\n")
	if err := ValidateForPublish(KindAgent, "corp-backend", missing); !errors.Is(err, ErrAgentNameMismatch) {
		t.Fatalf("缺 name 必须被拒，实际 %v", err)
	}
}

func TestValidateForPublish_agent仍要description(t *testing.T) {
	noDesc := agentFiles("---\nname: a\n---\n正文\n")
	if err := ValidateForPublish(KindAgent, "a", noDesc); !errors.Is(err, ErrNoDescription) {
		t.Fatalf("agent 也必须有 description，实际 %v", err)
	}
}

// 种子模板必须自带正确的 name，否则管理员发布时才发现，
// 而那时正文已经写进去了。
func TestSeedSkillMD_agent自带name(t *testing.T) {
	seed := SeedSkillMD(KindAgent, "corp-backend", "后端工程师")
	if !strings.Contains(seed, "name: corp-backend") {
		t.Fatalf("agent 模板必须种上 name，实际:\n%s", seed)
	}
	if err := ValidateForPublish(KindAgent, "corp-backend", agentFiles(seed)); err != nil {
		t.Fatalf("种出来的模板必须能直接通过发布校验: %v", err)
	}

	// skill 模板不该有 name
	if strings.Contains(SeedSkillMD(KindSkill, "x", "d"), "name:") {
		t.Error("skill 模板不该带 name")
	}
}

// remove 的形态取自 lock 的显式记录：manifest 里已经没有它了，无从反推。
func TestPlan_remove用lock记录的形态(t *testing.T) {
	lock := map[Ref]LockEntry{
		"agent/gone-agent": {BundleID: "b1", Kind: KindAgent, Shape: ShapeFile, Version: 1, Checksum: "c1"},
		"skill/gone-skill": {BundleID: "b2", Kind: KindSkill, Shape: ShapeDir, Version: 1, Checksum: "c2"},
	}
	items := Plan(nil, lock, map[Ref]Ownership{"agent/gone-agent": OwnMine, "skill/gone-skill": OwnMine})
	got := map[string]Shape{}
	for _, it := range items {
		if it.Action != ActionRemove {
			t.Fatalf("%s 应为 remove，实际 %s", it.Name, it.Action)
		}
		got[it.Name] = it.Shape
	}
	if got["gone-agent"] != ShapeFile || got["gone-skill"] != ShapeDir {
		t.Fatalf("形态取错会导致删错路径：%v", got)
	}
}

// 旧版 lock 没有 shape 字段时回退到按 kind 推，不能留空。
func TestPlan_旧lock缺shape时回退(t *testing.T) {
	lock := map[Ref]LockEntry{
		"agent/old": {BundleID: "b1", Kind: KindAgent, Version: 1, Checksum: "c1"},
	}
	items := Plan(nil, lock, map[Ref]Ownership{"agent/old": OwnMine})
	if items[0].Shape != ShapeFile {
		t.Fatalf("缺 shape 应按 kind 回退为 file，实际 %q", items[0].Shape)
	}
}

func TestPlan_manifest项按kind定形态(t *testing.T) {
	metas := []BundleMeta{
		{ID: "b1", Name: "ag", Kind: KindAgent, Version: 1, Checksum: "c1"},
		{ID: "b2", Name: "sk", Kind: KindSkill, Version: 1, Checksum: "c2"},
	}
	items := Plan(metas, map[Ref]LockEntry{}, map[Ref]Ownership{})
	for _, it := range items {
		want := ShapeDir
		if it.Name == "ag" {
			want = ShapeFile
		}
		if it.Shape != want {
			t.Errorf("%s 的形态应为 %s，实际 %s", it.Name, want, it.Shape)
		}
	}
}
