package resources

import (
	"errors"
	"testing"
)

func one(kind Kind, content string) []File {
	return []File{{Path: kind.EntryFile(), Content: content}}
}

func TestParseFrontmatter(t *testing.T) {
	fm, err := ParseFrontmatter("\uFEFF---\nname: x\ndescription: \"does things\"\ntags: [a, b]\nnested:\n  k: v\n---\nbody")
	if err != nil {
		t.Fatal(err)
	}
	if fm.Name != "x" || fm.Description != "does things" || len(fm.Tags) != 2 {
		t.Fatalf("解析错误: %+v", fm)
	}
	if _, err := ParseFrontmatter("no frontmatter"); !errors.Is(err, ErrNoFrontmatter) {
		t.Fatalf("缺 frontmatter: %v", err)
	}
	if _, err := ParseFrontmatter("---\nname: x\n"); !errors.Is(err, ErrBadFrontmatter) {
		t.Fatalf("未闭合: %v", err)
	}
	if fm, err := ParseFrontmatter("---\n---\nbody"); err != nil || fm.Name != "" {
		t.Fatalf("空 frontmatter 应合法: %v %+v", err, fm)
	}
}

func TestValidateForPublish_Skill(t *testing.T) {
	if err := ValidateForPublish(KindSkill, "deploy", one(KindSkill, "---\ndescription: d\n---\nbody")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateForPublish(KindSkill, "deploy", one(KindSkill, "---\nname: deploy\n---\nbody")); !errors.Is(err, ErrNoDescription) {
		t.Fatalf("缺 description: %v", err)
	}
	if err := ValidateForPublish(KindSkill, "deploy", one(KindSkill, "   ")); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("空内容: %v", err)
	}
}

func TestValidateForPublish_Agent(t *testing.T) {
	good := "name: reviewer\ndescription: reviews code\ninstructions: |\n  Be strict.\nmodel: sonnet\n"
	if err := ValidateForPublish(KindAgent, "reviewer", one(KindAgent, good)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateForPublish(KindAgent, "other", one(KindAgent, good)); !errors.Is(err, ErrAgentNameMismatch) {
		t.Fatalf("name 不一致: %v", err)
	}
	if err := ValidateForPublish(KindAgent, "reviewer", one(KindAgent, "name: reviewer\ndescription: x\n")); !errors.Is(err, ErrMissingField) {
		t.Fatalf("缺 instructions: %v", err)
	}
}

func TestValidateForPublish_HookMCPEnv(t *testing.T) {
	if err := ValidateForPublish(KindHook, "lint", one(KindHook, "id: lint\nevent: PreToolUse\nmatcher: Bash\ncommand: scripts/lint.sh\n")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateForPublish(KindHook, "lint", one(KindHook, "id: lint\n")); !errors.Is(err, ErrMissingField) {
		t.Fatalf("hook 缺字段: %v", err)
	}
	if err := ValidateForPublish(KindMCP, "corp-db", one(KindMCP, "transport: stdio\ncommand: npx\n")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateForPublish(KindMCP, "corp-db", one(KindMCP, "command: npx\n")); !errors.Is(err, ErrMissingField) {
		t.Fatalf("mcp 缺 transport: %v", err)
	}
	if err := ValidateForPublish(KindEnv, "API_BASE", one(KindEnv, "value: https://x\n")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateForPublish(KindEnv, "api_base", one(KindEnv, "value: x\n")); !errors.Is(err, ErrEnvNameFormat) {
		t.Fatalf("env 名格式: %v", err)
	}
}

func TestValidateForPublish_Learning(t *testing.T) {
	if err := ValidateForPublish(KindLearning, "port-conflict-2026-09-11-ab12", one(KindLearning, "---\ntitle: x\nauthor: a\n---\nbody")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateForPublish(KindLearning, "x", one(KindLearning, "---\nauthor: a\n---\nbody")); !errors.Is(err, ErrMissingField) {
		t.Fatalf("learning 缺 title: %v", err)
	}
}

func TestValidateForPublish_FilesFirst(t *testing.T) {
	// 文件集合非法时先于内容校验失败
	files := []File{{Path: "RULE.md", Content: "x"}, {Path: "extra.md", Content: "y"}}
	if err := ValidateForPublish(KindRule, "naming", files); !errors.Is(err, ErrExtraFiles) {
		t.Fatalf("多文件: %v", err)
	}
}

func TestValidateForPublish_EnvSecretRules(t *testing.T) {
	// 名字像凭据但没标 secret → 拒
	if err := ValidateForPublish(KindEnv, "API_TOKEN", one(KindEnv, "value: abc\n")); !errors.Is(err, ErrEnvNeedsSecretFlag) {
		t.Fatalf("API_TOKEN 未标 secret 应拒: %v", err)
	}
	// 标了 secret 又带值 → 拒
	if err := ValidateForPublish(KindEnv, "API_TOKEN", one(KindEnv, "secret: true\nvalue: abc\n")); !errors.Is(err, ErrSecretValueOnServer) {
		t.Fatalf("secret 带值应拒: %v", err)
	}
	// 标了 secret、无值 → 通过
	if err := ValidateForPublish(KindEnv, "API_TOKEN", one(KindEnv, "secret: true\ndescription: 网关令牌\n")); err != nil {
		t.Fatalf("secret 无值应通过: %v", err)
	}
	// 普通变量照旧
	if err := ValidateForPublish(KindEnv, "API_BASE", one(KindEnv, "value: https://x\n")); err != nil {
		t.Fatalf("普通变量应通过: %v", err)
	}
}

func TestValidateForPublish_ScansAllTextFiles(t *testing.T) {
	files := []File{
		{Path: "SKILL.md", Content: "---\ndescription: ok\n---\n"},
		{Path: "scripts/deploy.sh", Content: "export AWS_KEY=AKIAABCDEFGHIJKLMNOP\n"},
		{Path: "assets/logo.png", Content: "AKIAABCDEFGHIJKLMNOP"},
	}
	err := ValidateForPublish(KindSkill, "deploy", files)
	var se *SecretError
	if !errors.As(err, &se) {
		t.Fatalf("附属脚本里的密钥应被拦下: %v", err)
	}
	if len(se.Hits) != 1 || se.Hits[0].Path != "scripts/deploy.sh" {
		t.Fatalf("只应命中脚本文件（png 跳过）: %+v", se.Hits)
	}
}
