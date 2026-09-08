package core

import (
	"errors"
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	fm, err := ParseFrontmatter("---\ndescription: 审查数据库变更\ndisable-model-invocation: true\n---\n\n正文\n")
	if err != nil {
		t.Fatal(err)
	}
	if fm.Description != "审查数据库变更" {
		t.Fatalf("description: %q", fm.Description)
	}
	if !fm.DisableModelInvocation || !fm.HasDisableModelInvoke {
		t.Fatal("应识别出 disable-model-invocation: true")
	}
}

func TestParseFrontmatter_StripsQuotes(t *testing.T) {
	fm, _ := ParseFrontmatter("---\ndescription: \"带引号的说明\"\n---\n\nx")
	if fm.Description != "带引号的说明" {
		t.Fatalf("引号应被去掉: %q", fm.Description)
	}
}

func TestParseFrontmatter_IgnoresNestedAndComments(t *testing.T) {
	fm, err := ParseFrontmatter("---\n# 注释\ndescription: 顶层\nmetadata:\n  description: 嵌套的不算\n---\n\nx")
	if err != nil {
		t.Fatal(err)
	}
	if fm.Description != "顶层" {
		t.Fatalf("嵌套字段不应覆盖顶层: %q", fm.Description)
	}
}

func TestParseFrontmatter_Errors(t *testing.T) {
	if _, err := ParseFrontmatter("没有 frontmatter"); !errors.Is(err, ErrNoFrontmatter) {
		t.Fatalf("got %v", err)
	}
	if _, err := ParseFrontmatter("---\ndescription: x\n没有闭合"); !errors.Is(err, ErrBadFrontmatter) {
		t.Fatalf("got %v", err)
	}
}

func TestParseFrontmatter_ToleratesBOMAndCRLF(t *testing.T) {
	fm, err := ParseFrontmatter("\ufeff---\r\ndescription: 兼容\r\n---\r\n\r\nx")
	if err != nil || fm.Description != "兼容" {
		t.Fatalf("应容忍 BOM 与 CRLF: %q %v", fm.Description, err)
	}
}

// kind 只是后台展示用的标签，Claude Code 只认 frontmatter。
// 两者不一致时 kind 就成了谎话，必须在发布时挡下来。
func TestValidateForPublish_KindConsistency(t *testing.T) {
	cmdOK := []File{{Path: SkillFile, Content: "---\ndescription: d\ndisable-model-invocation: true\n---\n\nx"}}
	cmdBad := []File{{Path: SkillFile, Content: "---\ndescription: d\n---\n\nx"}}
	skillOK := []File{{Path: SkillFile, Content: "---\ndescription: d\n---\n\nx"}}
	skillBad := []File{{Path: SkillFile, Content: "---\ndescription: d\ndisable-model-invocation: true\n---\n\nx"}}

	if err := ValidateForPublish(KindCommand, "b", cmdOK); err != nil {
		t.Fatalf("command + 该字段应通过: %v", err)
	}
	if err := ValidateForPublish(KindSkill, "b", skillOK); err != nil {
		t.Fatalf("skill + 无该字段应通过: %v", err)
	}

	err := ValidateForPublish(KindCommand, "b", cmdBad)
	if !errors.Is(err, ErrKindMismatch) {
		t.Fatalf("command 缺该字段必须拒绝: %v", err)
	}
	if !strings.Contains(err.Error(), "仍会被模型自动调用") {
		t.Fatalf("错误信息应说清后果: %v", err)
	}

	if err := ValidateForPublish(KindSkill, "b", skillBad); !errors.Is(err, ErrKindMismatch) {
		t.Fatalf("skill 写了该字段必须拒绝: %v", err)
	}
}

func TestValidateForPublish_RequiresDescription(t *testing.T) {
	files := []File{{Path: SkillFile, Content: "---\n---\n\nx"}}
	if err := ValidateForPublish(KindSkill, "b", files); !errors.Is(err, ErrNoDescription) {
		t.Fatalf("缺 description 必须拒绝: %v", err)
	}
}

func TestValidateForPublish_RequiresFrontmatter(t *testing.T) {
	files := []File{{Path: SkillFile, Content: "只有正文没有 frontmatter"}}
	if err := ValidateForPublish(KindSkill, "b", files); !errors.Is(err, ErrNoFrontmatter) {
		t.Fatalf("缺 frontmatter 必须拒绝: %v", err)
	}
}

// 种出来的模板必须自己就能通过发布校验，否则新建即是死路。
func TestSeedSkillMD_PassesItsOwnValidation(t *testing.T) {
	for _, k := range []Kind{KindSkill, KindCommand} {
		files := []File{{Path: SkillFile, Content: SeedSkillMD(k, "b", "测试说明")}}
		if err := ValidateForPublish(k, "b", files); err != nil {
			t.Fatalf("%s 的模板应能直接发布: %v", k, err)
		}
	}
}

func TestSeedSkillMD_EmptyDescriptionStillValid(t *testing.T) {
	files := []File{{Path: SkillFile, Content: SeedSkillMD(KindSkill, "b", "")}}
	if err := ValidateForPublish(KindSkill, "b", files); err != nil {
		t.Fatalf("未填说明时模板也应有占位 description: %v", err)
	}
}
