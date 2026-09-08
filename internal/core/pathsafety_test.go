package core

import (
	"errors"
	"testing"
)

func TestValidateName(t *testing.T) {
	ok := []string{"corp-common", "a", "a1", "deploy", "corp-backend-toolkit"}
	for _, n := range ok {
		if err := ValidateName(n); err != nil {
			t.Errorf("%q 应合法: %v", n, err)
		}
	}

	bad := map[string]error{
		"":             ErrEmptyName,
		"synced":       ErrNameReserved,
		"Synced":       ErrNameReserved, // 任意大小写（PRD C18）
		"SYNCED":       ErrNameReserved,
		"Corp-Common":  ErrNameFormat, // 大写
		"corp_common":  ErrNameFormat, // 下划线
		"-corp":        ErrNameFormat,
		"corp-":        ErrNameFormat,
		"corp/common":  ErrNameFormat,
		"..":           ErrNameFormat,
		".":            ErrNameFormat,
		"corp common":  ErrNameFormat,
		"corp\\common": ErrNameFormat,
	}
	for n, want := range bad {
		err := ValidateName(n)
		if err == nil {
			t.Errorf("%q 应非法", n)
			continue
		}
		if !errors.Is(err, want) {
			t.Errorf("%q: got %v, want %v", n, err, want)
		}
	}
}

func TestValidateName_TooLong(t *testing.T) {
	long := ""
	for i := 0; i < maxNameLen+1; i++ {
		long += "a"
	}
	if err := ValidateName(long); !errors.Is(err, ErrNameTooLong) {
		t.Fatalf("超长名应拒绝: %v", err)
	}
}

func TestValidateFiles_OK(t *testing.T) {
	files := []File{
		{Path: "SKILL.md", Content: "x"},
		{Path: "references/api.md", Content: "y"},
		{Path: "scripts/run.sh", Content: "z"},
	}
	if err := ValidateFiles(KindSkill, files); err != nil {
		t.Fatal(err)
	}
}

func TestValidateFiles_RequiresSkillMD(t *testing.T) {
	if err := ValidateFiles(KindSkill, nil); !errors.Is(err, ErrNoSkillMD) {
		t.Fatalf("空集合应拒绝: %v", err)
	}
	files := []File{{Path: "readme.md", Content: "x"}}
	if err := ValidateFiles(KindSkill, files); !errors.Is(err, ErrNoSkillMD) {
		t.Fatalf("缺 SKILL.md 应拒绝: %v", err)
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
		MarkerFile:          ErrPathHidden,
	}
	for p, want := range cases {
		files := []File{{Path: "SKILL.md", Content: "x"}, {Path: p, Content: "y"}}
		err := ValidateFiles(KindSkill, files)
		if err == nil {
			t.Errorf("%q 应被拒绝", p)
			continue
		}
		if !errors.Is(err, want) {
			t.Errorf("%q: got %v, want %v", p, err, want)
		}
	}
}

func TestValidateFiles_RejectsDuplicatePath(t *testing.T) {
	files := []File{
		{Path: "SKILL.md", Content: "x"},
		{Path: "SKILL.md", Content: "y"},
	}
	if err := ValidateFiles(KindSkill, files); !errors.Is(err, ErrPathDuplicate) {
		t.Fatalf("重复路径应拒绝: %v", err)
	}
}

func TestKindValid(t *testing.T) {
	if !KindCommand.Valid() || !KindSkill.Valid() {
		t.Fatal("command/skill 应合法")
	}
	if Kind("plugin").Valid() {
		t.Fatal("未知 kind 应非法")
	}
}
