package teamaifmt

import (
	"strings"
	"testing"

	"github.com/ith5/ith5/internal/resources"
	"github.com/ith5/ith5/internal/sync"
)

func mkEntry(kind resources.Kind, name, ns string, files map[string]string, blobs map[string][]byte) resources.Entry {
	e := resources.Entry{Kind: kind, Name: name, Namespace: ns, Status: resources.StatusOK}
	for path, content := range files {
		sum := resources.BlobSum([]byte(content))
		blobs[sum] = []byte(content)
		e.Files = append(e.Files, resources.FileRef{Path: path, SHA256: sum, Size: len(content)})
	}
	return e
}

func TestRender_Layout(t *testing.T) {
	blobs := map[string][]byte{}
	snap := sync.Snapshot{
		Org:    sync.OrgRef{Slug: "demo", Name: "Demo \"Org\""},
		Policy: resources.Policy{EnforcedRules: []string{"security"}, HooksAutoApply: true, MCPAllowedHosts: []string{"*.corp"}},
	}
	snap.Resources = []resources.Entry{
		mkEntry(resources.KindSkill, "deploy", "common", map[string]string{"SKILL.md": "---\ndescription: d\n---\nx", "references/a.md": "ref"}, blobs),
		mkEntry(resources.KindRule, "naming", "common", map[string]string{"RULE.md": "# naming"}, blobs),
		mkEntry(resources.KindDoc, "arch/overview.md", "common", map[string]string{"DOC.md": "# arch"}, blobs),
		mkEntry(resources.KindAgent, "reviewer", "common", map[string]string{"AGENT.yaml": "name: reviewer\n"}, blobs),
		mkEntry(resources.KindClaudeMD, "ctx", "billing", map[string]string{"CLAUDEMD.md": "## ctx"}, blobs),
		mkEntry(resources.KindLearning, "l1", "common", map[string]string{"LEARNING.md": "---\ntitle: t\n---\nbody"}, blobs),
		mkEntry(resources.KindEnv, "API_BASE", "common", map[string]string{"ENV.yaml": "value: https://x\ndescription: api\n"}, blobs),
		mkEntry(resources.KindHook, "lint", "common", map[string]string{"HOOK.yaml": "id: lint\nevent: PostToolUse\ncommand: scripts/lint.sh\n", "scripts/lint.sh": "#!/bin/sh"}, blobs),
		mkEntry(resources.KindMCP, "docs", "common", map[string]string{"MCP.yaml": "name: docs\ntransport: http\nurl: https://m\n"}, blobs),
	}
	snap.Resources[0].Tags = []string{"deploy", "ops"}
	conflict := resources.Entry{Kind: resources.KindRule, Name: "clash", Status: resources.StatusConflict}
	snap.Resources = append(snap.Resources, conflict)
	cul := mkEntry(resources.KindCulture, "culture", "common", map[string]string{"CULTURE.md": "---\ncompany:\n  name: Demo\n---\nbe kind"}, blobs)
	snap.Culture = &cul

	files, err := Render(snap, func(sha string) ([]byte, error) { return blobs[sha], nil })
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"skills/deploy/SKILL.md":        "---\ndescription: d\n---\nx",
		"skills/deploy/references/a.md": "ref",
		"rules/naming.md":               "# naming",
		"docs/arch/overview.md":         "# arch",
		"agents/reviewer.yaml":          "name: reviewer\n",
		"claudemd/billing/ctx.md":       "## ctx",
		"learnings/l1.md":               "---\ntitle: t\n---\nbody",
		"hooks/scripts/lint.sh":         "#!/bin/sh",
		"culture.md":                    "---\ncompany:\n  name: Demo\n---\nbe kind",
	}
	for p, c := range want {
		if string(files[p]) != c {
			t.Errorf("%s 内容错误: %q", p, files[p])
		}
	}
	if _, ok := files["rules/clash.md"]; ok {
		t.Error("冲突条目不应渲染")
	}
	env := string(files["env/env.yaml"])
	if !strings.HasPrefix(env, "variables:\n  - key: API_BASE\n    value: https://x\n    description: api\n") {
		t.Errorf("env.yaml 格式: %q", env)
	}
	hooks := string(files["hooks/hooks.yaml"])
	if !strings.HasPrefix(hooks, "hooks:\n  - id: lint\n    event: PostToolUse\n    command: scripts/lint.sh\n") {
		t.Errorf("hooks.yaml 格式: %q", hooks)
	}
	mcp := string(files["mcp/mcp.yaml"])
	if !strings.HasPrefix(mcp, "servers:\n  - name: docs\n    transport: http\n") {
		t.Errorf("mcp.yaml 格式: %q", mcp)
	}
	ty := string(files["teamai.yaml"])
	for _, s := range []string{`team: "demo"`, `description: "Demo \"Org\""`, "mode: self", `enforced: ["security"]`, "autoApply: true", `allowedHosts: ["*.corp"]`} {
		if !strings.Contains(ty, s) {
			t.Errorf("teamai.yaml 缺少 %q:\n%s", s, ty)
		}
	}
	tags := string(files["tags.yaml"])
	if !strings.Contains(tags, `deploy: ["deploy", "ops"]`) {
		t.Errorf("tags.yaml: %q", tags)
	}
}

func TestRender_MissingBlobFails(t *testing.T) {
	blobs := map[string][]byte{}
	snap := sync.Snapshot{Resources: []resources.Entry{mkEntry(resources.KindRule, "r", "common", map[string]string{"RULE.md": "x"}, blobs)}}
	_, err := Render(snap, func(string) ([]byte, error) { return nil, strings.NewReader("").UnreadByte() })
	if err == nil {
		t.Fatal("取不到 blob 应报错，不能写出半份")
	}
}

func TestRenderNamespaced(t *testing.T) {
	blobs := map[string][]byte{}
	snap := sync.Snapshot{
		Org:      sync.OrgRef{Slug: "demo", Name: "Demo"},
		Projects: []sync.ProjectRef{{ID: "p1", Slug: "billing", Name: "计费"}},
		Grants:   []sync.GrantRef{{GroupKey: "backend-pack", Name: "后端包"}},
		Resources: []resources.Entry{
			mkEntry(resources.KindSkill, "deploy", "billing", map[string]string{"SKILL.md": "---\ndescription: d\n---\n"}, blobs),
			mkEntry(resources.KindSkill, "shared", "common", map[string]string{"SKILL.md": "---\ndescription: s\n---\n"}, blobs),
			mkEntry(resources.KindRule, "naming", "billing", map[string]string{"RULE.md": "# n\n"}, blobs),
			mkEntry(resources.KindRule, "base", "common", map[string]string{"RULE.md": "# b\n"}, blobs),
			mkEntry(resources.KindLearning, "l1", "billing", map[string]string{"LEARNING.md": "---\ntitle: t\n---\n"}, blobs),
		},
	}
	snap.Resources[0].Level, snap.Resources[2].Level, snap.Resources[4].Level = resources.LevelProject, resources.LevelProject, resources.LevelProject
	snap.Resources[1].Level, snap.Resources[3].Level = resources.LevelOrg, resources.LevelOrg
	files, err := RenderWith(snap, func(sha string) ([]byte, error) { return blobs[sha], nil }, Options{Namespaced: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"skills/billing/deploy/SKILL.md", "skills/common/shared/SKILL.md", "rules/billing/naming.md", "rules/base.md", "learnings/billing/l1.md", "manifest/roles.yaml", "manifest/projects.yaml"} {
		if _, ok := files[p]; !ok {
			t.Errorf("缺少 %s；有: %v", p, keys(files))
		}
	}
	if roles := string(files["manifest/roles.yaml"]); !strings.Contains(roles, `- id: "backend-pack"`) || !strings.Contains(roles, `skills: ["backend-pack"]`) {
		t.Errorf("roles.yaml: %q", roles)
	}
	if projects := string(files["manifest/projects.yaml"]); !strings.Contains(projects, `- id: "billing"`) || !strings.Contains(projects, `learnings: ["billing"]`) {
		t.Errorf("projects.yaml: %q", projects)
	}
	// 扁平布局不写 manifest
	flat, _ := Render(snap, func(sha string) ([]byte, error) { return blobs[sha], nil })
	if _, ok := flat["manifest/roles.yaml"]; ok {
		t.Error("扁平布局不该有 manifest")
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
