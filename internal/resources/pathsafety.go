package resources

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

var (
	ErrEmptyName        = errors.New("资源名不能为空")
	ErrNameFormat       = errors.New("资源名必须是小写 slug（字母、数字、连字符），且以字母或数字开头结尾")
	ErrNameReserved     = errors.New("资源名被保留")
	ErrNameTooLong      = errors.New("资源名过长")
	ErrSingletonName    = errors.New("该类型的资源名固定，不可更改")
	ErrEnvNameFormat    = errors.New("环境变量名必须是大写字母、数字与下划线，且以字母开头")
	ErrNoEntryFile      = errors.New("缺少入口文件")
	ErrExtraFiles       = errors.New("该类型只允许一个文件")
	ErrHookScriptPath   = errors.New("hook 的附加文件必须放在 scripts/ 下")
	ErrBadKind          = errors.New("未知的资源类型")
	ErrBadLevel         = errors.New("未知的层级")
	ErrPathAbsolute     = errors.New("文件路径必须是相对路径")
	ErrPathTraversal    = errors.New("文件路径不得包含 ..")
	ErrPathNUL          = errors.New("文件路径不得包含 NUL")
	ErrPathBackslash    = errors.New("文件路径必须使用 / 分隔")
	ErrPathNotClean     = errors.New("文件路径必须是规范形式")
	ErrPathDuplicate    = errors.New("文件路径重复")
	ErrPathEmpty        = errors.New("文件路径不能为空")
	ErrPathHidden       = errors.New("文件路径不得以 . 开头")
	ErrPathCaseConflict = errors.New("文件路径在大小写不敏感的文件系统上会冲突")
)

const (
	maxNameLen = 64
	maxDocLen  = 200
)

// reservedNames 不允许作为 skill 名：synced 被 Claude Code 保留用于同步账号技能。
var reservedNames = map[string]bool{"synced": true}

var (
	slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	envRe  = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

// ValidateName 校验资源名。规则按类型分流：
//   - env：大写变量名
//   - doc：相对路径，每段都是 slug 或带扩展名的 slug
//   - culture / policy：固定名
//   - 其余：小写 slug
func ValidateName(kind Kind, name string) error {
	if !kind.Valid() {
		return fmt.Errorf("%w: %q", ErrBadKind, kind)
	}
	if name == "" {
		return ErrEmptyName
	}
	switch kind {
	case KindEnv:
		if len(name) > maxNameLen {
			return fmt.Errorf("%w: %d > %d", ErrNameTooLong, len(name), maxNameLen)
		}
		if !envRe.MatchString(name) {
			return fmt.Errorf("%w: %q", ErrEnvNameFormat, name)
		}
		return nil
	case KindCulture, KindPolicy:
		if name != string(kind) {
			return fmt.Errorf("%w: 应为 %q", ErrSingletonName, kind)
		}
		return nil
	case KindDoc:
		if len(name) > maxDocLen {
			return fmt.Errorf("%w: %d > %d", ErrNameTooLong, len(name), maxDocLen)
		}
		if err := validatePath(name); err != nil {
			return err
		}
		for _, seg := range strings.Split(name, "/") {
			base := strings.TrimSuffix(seg, path.Ext(seg))
			if !slugRe.MatchString(base) {
				return fmt.Errorf("%w: %q", ErrNameFormat, name)
			}
		}
		return nil
	}
	if len(name) > maxNameLen {
		return fmt.Errorf("%w: %d > %d", ErrNameTooLong, len(name), maxNameLen)
	}
	if kind == KindSkill && reservedNames[strings.ToLower(name)] {
		return fmt.Errorf("%w: %q", ErrNameReserved, name)
	}
	if !slugRe.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrNameFormat, name)
	}
	return nil
}

// ValidateFiles 校验一个版本的文件集合。
//
// 任何一项失败都是版本级失败，不允许「能写几个就先写几个」。
//   - 每个路径都必须安全、规范、不重复、大小写不冲突
//   - 必须包含该类型的入口文件
//   - 不允许多文件的类型只能有入口文件
//   - hook 的附加文件必须在 scripts/ 下
func ValidateFiles(kind Kind, paths []string) error {
	if !kind.Valid() {
		return fmt.Errorf("%w: %q", ErrBadKind, kind)
	}
	entry := kind.EntryFile()
	if len(paths) == 0 {
		return fmt.Errorf("%w: %s", ErrNoEntryFile, entry)
	}
	seen := make(map[string]bool, len(paths))
	folded := make(map[string]string, len(paths))
	for _, p := range paths {
		if err := validatePath(p); err != nil {
			return fmt.Errorf("%q: %w", p, err)
		}
		if seen[p] {
			return fmt.Errorf("%w: %q", ErrPathDuplicate, p)
		}
		seen[p] = true
		lower := strings.ToLower(p)
		if other, ok := folded[lower]; ok {
			return fmt.Errorf("%w: %q 与 %q", ErrPathCaseConflict, p, other)
		}
		folded[lower] = p
	}
	if !seen[entry] {
		return fmt.Errorf("%w: %s", ErrNoEntryFile, entry)
	}
	if !kind.AllowsExtraFiles() && len(paths) != 1 {
		return fmt.Errorf("%w，入口为 %s", ErrExtraFiles, entry)
	}
	if kind == KindHook {
		for p := range seen {
			if p != entry && !strings.HasPrefix(p, "scripts/") {
				return fmt.Errorf("%w: %q", ErrHookScriptPath, p)
			}
		}
	}
	return nil
}

// validatePath 拒绝一切可能逃出目标目录或在某些平台上出问题的路径。
func validatePath(p string) error {
	if p == "" {
		return ErrPathEmpty
	}
	if strings.ContainsRune(p, 0) {
		return ErrPathNUL
	}
	if strings.Contains(p, `\`) {
		return ErrPathBackslash
	}
	if strings.HasPrefix(p, "/") {
		return ErrPathAbsolute
	}
	// Windows 盘符（C:foo）与 UNC 已被反斜杠规则挡住一半，这里补上盘符
	if len(p) >= 2 && p[1] == ':' {
		return ErrPathAbsolute
	}
	for _, seg := range strings.Split(p, "/") {
		switch {
		case seg == "..":
			return ErrPathTraversal
		case seg == "." || seg == "":
			return ErrPathNotClean
		case strings.HasPrefix(seg, "."):
			return ErrPathHidden
		}
	}
	if path.Clean(p) != p {
		return ErrPathNotClean
	}
	return nil
}
