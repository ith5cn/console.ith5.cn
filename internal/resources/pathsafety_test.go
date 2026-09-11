package resources

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateName_Slug(t *testing.T) {
	for _, n := range []string{"corp-common", "a", "a1", "deploy", "corp-backend-toolkit"} {
		if err := ValidateName(KindSkill, n); err != nil {
			t.Errorf("%q 应合法: %v", n, err)
		}
	}
	bad := map[string]error{
		"":             ErrEmptyName,
		"synced":       ErrNameReserved,
		"Synced":       ErrNameReserved,
		"Corp-Common":  ErrNameFormat,
		"corp_common":  ErrNameFormat,
		"-corp":        ErrNameFormat,
		"corp-":        ErrNameFormat,
		"corp/common":  ErrNameFormat,
		"..":           ErrNameFormat,
		"corp common":  ErrNameFormat,
		"corp\\common": ErrNameFormat,
	}
	for n, want := range bad {
		if err := ValidateName(KindSkill, n); !errors.Is(err, want) {
			t.Errorf("%q: got %v, want %v", n, err, want)
		}
	}
	if err := ValidateName(KindSkill, strings.Repeat("a", maxNameLen+1)); !errors.Is(err, ErrNameTooLong) {
		t.Fatalf("超长名应拒绝: %v", err)
	}
	// synced 只对 skill 保留
	if err := ValidateName(KindRule, "synced"); err != nil {
		t.Fatalf("rule 名 synced 应合法: %v", err)
	}
}

func TestValidateName_Env(t *testing.T) {
	for _, n := range []string{"API_BASE", "A", "X1_Y2"} {
		if err := ValidateName(KindEnv, n); err != nil {
			t.Errorf("%q 应合法: %v", n, err)
		}
	}
	for _, n := range []string{"api_base", "1ABC", "A-B", "A B", ""} {
		if err := ValidateName(KindEnv, n); err == nil {
			t.Errorf("%q 应非法", n)
		}
	}
}

func TestValidateName_Singleton(t *testing.T) {
	if err := ValidateName(KindCulture, "culture"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateName(KindPolicy, "policy"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateName(KindCulture, "other"); !errors.Is(err, ErrSingletonName) {
		t.Fatalf("单例名固定: %v", err)
	}
}

func TestValidateName_Doc(t *testing.T) {
	for _, n := range []string{"overview.md", "arch/overview.md", "api/v2/errors.md", "readme"} {
		if err := ValidateName(KindDoc, n); err != nil {
			t.Errorf("%q 应合法: %v", n, err)
		}
	}
	for _, n := range []string{"../x.md", "/abs.md", "Arch/Overview.md", "a//b.md", ".hidden.md"} {
		if err := ValidateName(KindDoc, n); err == nil {
			t.Errorf("%q 应非法", n)
		}
	}
}

func TestValidateName_BadKind(t *testing.T) {
	if err := ValidateName(Kind("plugin"), "x"); !errors.Is(err, ErrBadKind) {
		t.Fatalf("未知 kind: %v", err)
	}
}

func TestValidateFiles_Skill(t *testing.T) {
	if err := ValidateFiles(KindSkill, []string{"SKILL.md", "references/api.md", "scripts/run.sh"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFiles(KindSkill, nil); !errors.Is(err, ErrNoEntryFile) {
		t.Fatalf("空集合应拒绝: %v", err)
	}
	if err := ValidateFiles(KindSkill, []string{"readme.md"}); !errors.Is(err, ErrNoEntryFile) {
		t.Fatalf("缺 SKILL.md 应拒绝: %v", err)
	}
}

func TestValidateFiles_SingleFileKinds(t *testing.T) {
	for _, k := range []Kind{KindRule, KindDoc, KindAgent, KindMCP, KindEnv, KindClaudeMD, KindCulture, KindPolicy, KindLearning} {
		if err := ValidateFiles(k, []string{k.EntryFile()}); err != nil {
			t.Errorf("%s: %v", k, err)
		}
		if err := ValidateFiles(k, []string{k.EntryFile(), "extra.md"}); !errors.Is(err, ErrExtraFiles) {
			t.Errorf("%s 多文件应拒绝: %v", k, err)
		}
	}
}

func TestValidateFiles_HookScripts(t *testing.T) {
	if err := ValidateFiles(KindHook, []string{"HOOK.yaml", "scripts/check.sh"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFiles(KindHook, []string{"HOOK.yaml", "check.sh"}); !errors.Is(err, ErrHookScriptPath) {
		t.Fatalf("脚本必须在 scripts/ 下: %v", err)
	}
}

func TestValidateFiles_RejectsDangerousPaths(t *testing.T) {
	cases := map[string]error{
		"/etc/passwd":       ErrPathAbsolute,
		"C:/win":            ErrPathAbsolute,
		"../escape.md":      ErrPathTraversal,
		"a/../../escape.md": ErrPathTraversal,
		"a\\b.md":           ErrPathBackslash,
		"a//b.md":           ErrPathNotClean,
		"./a.md":            ErrPathNotClean,
		"":                  ErrPathEmpty,
		"a\x00b":            ErrPathNUL,
		".git/config":       ErrPathHidden,
	}
	for p, want := range cases {
		err := ValidateFiles(KindSkill, []string{"SKILL.md", p})
		if !errors.Is(err, want) {
			t.Errorf("%q: got %v, want %v", p, err, want)
		}
	}
}

func TestValidateFiles_DuplicateAndCase(t *testing.T) {
	if err := ValidateFiles(KindSkill, []string{"SKILL.md", "SKILL.md"}); !errors.Is(err, ErrPathDuplicate) {
		t.Fatalf("重复路径: %v", err)
	}
	if err := ValidateFiles(KindSkill, []string{"SKILL.md", "a.md", "A.md"}); !errors.Is(err, ErrPathCaseConflict) {
		t.Fatalf("大小写冲突: %v", err)
	}
}

func TestKind(t *testing.T) {
	for _, k := range AllKinds {
		if !k.Valid() || k.EntryFile() == "" {
			t.Errorf("%s 应合法且有入口文件", k)
		}
	}
	if Kind("plugin").Valid() {
		t.Fatal("未知 kind 应非法")
	}
	if !KindPolicy.Singleton() || KindSkill.Singleton() {
		t.Fatal("单例判断错误")
	}
	if LevelProject.Specificity() <= LevelTeam.Specificity() || LevelTeam.Specificity() <= LevelOrg.Specificity() {
		t.Fatal("层级具体度顺序错误")
	}
	r := MakeRef(KindAgent, "reviewer")
	if r.Kind() != KindAgent || r.Name() != "reviewer" {
		t.Fatalf("Ref 拆分错误: %s", r)
	}
}
