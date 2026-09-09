package core

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

var (
	ErrEmptyName     = errors.New("bundle 名不能为空")
	ErrNameFormat    = errors.New("bundle 名必须是小写 slug（字母、数字、连字符），且以字母或数字开头结尾")
	ErrNameReserved  = errors.New("bundle 名被 Claude Code 保留")
	ErrNameTooLong   = errors.New("bundle 名过长")
	ErrNoSkillMD     = errors.New("bundle 必须包含 SKILL.md")
	ErrNoAgentMD     = errors.New("该类型必须且只能包含一个文件")
	ErrNotJSONObject = errors.New("内容必须是一个 JSON 对象")
	ErrNoMCPServers  = errors.New("MCP 配置必须含顶层 mcpServers 对象，且至少一个 server")
	ErrBadKind       = errors.New("未知的 bundle 类型")
	ErrPathAbsolute  = errors.New("文件路径必须是相对路径")
	ErrPathTraversal = errors.New("文件路径不得包含 ..")
	ErrPathNUL       = errors.New("文件路径不得包含 NUL")
	ErrPathBackslash = errors.New("文件路径必须使用 / 分隔")
	ErrPathNotClean  = errors.New("文件路径必须是规范形式")
	ErrPathDuplicate = errors.New("文件路径重复")
	ErrPathEmpty     = errors.New("文件路径不能为空")
	ErrPathHidden    = errors.New("文件路径不得占用 ITH5 保留名")
)

const maxNameLen = 64

// SkillFile 是目录形态 Bundle 必须包含的入口文件名。
const SkillFile = "SKILL.md"

// 文件形态与合并形态 Bundle 的唯一文件名（技术方案 §8.2.1b）。
//
// 它们在 store 里用固定名，物化到用户机器上时才改成 <bundle><ext> ——
// 固定名是为了让内容寻址与去重不受 bundle 名影响。
const (
	AgentFile    = "AGENT.md"
	HookFile     = "HOOK.js"
	WorkflowFile = "WORKFLOW.js"
	StandardFile = "STANDARD.md"
	SettingFile  = "SETTING.json"
	MCPFile      = "MCP.json"
)

// MarkerFile 是 copy 策略下标记目录归属的文件（技术方案 §8.8）。
// 它由 CLI 写入，Bundle 内容中不允许出现同名文件。
const MarkerFile = ".ith5-bundle.json"

// reservedNames 是不允许作为 bundle 名的名称。
//
// synced 被 Claude Code 保留用于同步 claude.ai 账号技能
// （CLAUDE_CODE_SYNC_SKILLS），我方写入会被静默跳过（PRD C18）。
var reservedNames = map[string]bool{
	"synced": true,
}

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidateName 校验 bundle 名是否可安全用作目录名与调用名。
func ValidateName(name string) error {
	if name == "" {
		return ErrEmptyName
	}
	if len(name) > maxNameLen {
		return fmt.Errorf("%w: %d > %d", ErrNameTooLong, len(name), maxNameLen)
	}
	if reservedNames[strings.ToLower(name)] {
		return fmt.Errorf("%w: %q", ErrNameReserved, name)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrNameFormat, name)
	}
	return nil
}

// ValidateFiles 校验一个 Bundle 版本的文件集合。
//
// 任何一项失败都是 Bundle 级失败——不允许“能写几个就先写几个”
// （技术方案 §8.2.2）。
//
// 校验按**形态**分流（§5.5）：
//   - 目录形态：至少包含 SKILL.md，其余文件不限
//   - 文件形态：必须且只能包含一个入口文件（AGENT.md / HOOK.js / ...）
//   - 合并形态：同上，入口文件是一份 JSON 片段
//
// kind 是必需参数而非可选：这是「错了会出安全问题」的逻辑，
// 不能让调用方通过省略参数拿到一个宽松的默认行为。
func ValidateFiles(kind Kind, files []File) error {
	if !kind.Valid() {
		return fmt.Errorf("%w: %q", ErrBadKind, kind)
	}
	if len(files) == 0 {
		return ErrNoSkillMD
	}
	seen := make(map[string]bool, len(files))

	for _, f := range files {
		if err := validatePath(f.Path); err != nil {
			return fmt.Errorf("%q: %w", f.Path, err)
		}
		if seen[f.Path] {
			return fmt.Errorf("%w: %q", ErrPathDuplicate, f.Path)
		}
		seen[f.Path] = true
	}

	if shape := kind.Shape(); shape == ShapeFile || shape == ShapeMerge {
		// 「只能一个文件」不是洁癖：多出来的文件在物化时无处安放
		// ——文件形态的目标是一个文件，没有目录可以承载支持文件；
		// 合并形态干脆没有目标目录。
		// 允许它们进 store 只会造成「发布成功、内容却下不去」。
		entry := kind.EntryFile()
		if len(files) != 1 || !seen[entry] {
			return fmt.Errorf("%w，名为 %s", ErrNoAgentMD, entry)
		}
		return nil
	}
	if !seen[SkillFile] {
		return ErrNoSkillMD
	}
	return nil
}

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
	// Windows 盘符，如 C:foo
	if len(p) >= 2 && p[1] == ':' {
		return ErrPathAbsolute
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return ErrPathTraversal
		}
	}
	// path.Clean 能消除 ./ 与重复斜杠；不等则说明不是规范形式。
	if path.Clean(p) != p {
		return ErrPathNotClean
	}
	if p == MarkerFile {
		return fmt.Errorf("%w: %q", ErrPathHidden, MarkerFile)
	}
	return nil
}
