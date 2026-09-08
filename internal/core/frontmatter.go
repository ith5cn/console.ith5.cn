package core

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrKindMismatch      = errors.New("kind 与 frontmatter 不一致")
	ErrNoFrontmatter     = errors.New("入口文件缺少 frontmatter")
	ErrNoDescription     = errors.New("frontmatter 缺少 description")
	ErrBadFrontmatter    = errors.New("frontmatter 未闭合")
	ErrAgentNameMismatch = errors.New("AGENT.md 的 frontmatter name 与 bundle 名不一致")
)

// disableModelInvocation 是让技能「只有用户敲 /名称 才触发」的字段。
// 它是 Claude Code 里 command 与 skill 的**唯一实际差别**——
// 两者的落盘路径、文件名、目录结构完全相同。
const disableModelInvocation = "disable-model-invocation"

// Frontmatter 是我们关心的那几个字段。
//
// 这里刻意不引 YAML 库：core 保持零依赖（技术方案 §4），
// 而我们只需要判断少数几个顶层标量字段是否存在及其真假。
type Frontmatter struct {
	Name                   string
	Description            string
	DisableModelInvocation bool
	HasDisableModelInvoke  bool
}

// ParseFrontmatter 从 SKILL.md 正文中解析 frontmatter。
//
// 只处理顶层的 `key: value` 形式。嵌套结构、多行字符串等一律忽略
// —— 我们不需要理解它们，只需要不误判。
func ParseFrontmatter(content string) (Frontmatter, error) {
	var fm Frontmatter
	s := strings.TrimLeft(content, "\ufeff \t\r\n")
	if !strings.HasPrefix(s, "---") {
		return fm, ErrNoFrontmatter
	}
	rest := s[3:]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	} else {
		return fm, ErrBadFrontmatter
	}
	// 空 frontmatter（--- 紧跟 ---）的闭合符在位置 0，前面没有换行
	var block string
	switch {
	case strings.HasPrefix(rest, "---"):
		block = ""
	default:
		end := strings.Index(rest, "\n---")
		if end < 0 {
			return fm, ErrBadFrontmatter
		}
		block = rest[:end]
	}

	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		// 缩进行属于上一个键的嵌套内容，跳过
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") ||
			strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		switch key {
		case "name":
			fm.Name = val
		case "description":
			fm.Description = val
		case disableModelInvocation:
			fm.HasDisableModelInvoke = true
			fm.DisableModelInvocation = val == "true" || val == "yes" || val == "on"
		}
	}
	return fm, nil
}

// ValidateForPublish 在发布前校验内容与声明的一致性。
//
// 存在的理由：`kind` 只是后台展示用的标签，Claude Code 只认 frontmatter。
// 若管理员选了 command 却没写 disable-model-invocation，这个技能仍会被
// 模型自动调用 —— kind 就成了一句谎话。宁可在发布时挡下来，
// 也不要让管理员以为自己配了个「只能手动触发」的命令。
func ValidateForPublish(kind Kind, bundleName string, files []File) error {
	if err := ValidateFiles(kind, files); err != nil {
		return err
	}
	entry := kind.Shape().EntryFile()
	var content string
	for _, f := range files {
		if f.Path == entry {
			content = f.Content
			break
		}
	}
	fm, err := ParseFrontmatter(content)
	if err != nil {
		return fmt.Errorf("%s: %w（至少需要 ---\\ndescription: ...\\n---）", entry, err)
	}
	if fm.Description == "" {
		return fmt.Errorf("%s: %w", entry, ErrNoDescription)
	}
	switch kind {
	case KindCommand:
		if !fm.DisableModelInvocation {
			return fmt.Errorf("%w: 类型为 command 时，SKILL.md 必须包含 `%s: true`，"+
				"否则它仍会被模型自动调用", ErrKindMismatch, disableModelInvocation)
		}
	case KindSkill:
		if fm.DisableModelInvocation {
			return fmt.Errorf("%w: 类型为 skill 但 SKILL.md 写了 `%s: true`，"+
				"模型将无法自动调用它；请改成 command，或删掉该字段",
				ErrKindMismatch, disableModelInvocation)
		}
	case KindAgent:
		// 派发方按 bundle 名写 subagent_type，Claude Code 按 frontmatter 的
		// name 注册。两者对不上时 agent **装得上但永远派发不到**，而且本地
		// 看不出任何异常——不会报错、不会缺文件、doctor 也查不出来。
		// 这类静默失效只能挡在发布口，挡不住就会变成
		// 「同步显示成功、用起来说没有这个 agent」的工单。
		if fm.Name == "" {
			return fmt.Errorf("%w: AGENT.md 的 frontmatter 必须写 `name: %s`",
				ErrAgentNameMismatch, bundleName)
		}
		if fm.Name != bundleName {
			return fmt.Errorf("%w: frontmatter 写的是 %q，bundle 名是 %q。"+
				"派发时用的是 bundle 名，不改的话这个 agent 永远派发不到",
				ErrAgentNameMismatch, fm.Name, bundleName)
		}
	}
	return nil
}

// SeedSkillMD 为新建的 bundle 生成一份可直接编辑的 SKILL.md 模板。
//
// 新建出来若是空的，管理员必须自己知道「文件名必须叫 SKILL.md」，
// 打错就发布失败 —— 那是个陷阱。模板同时保证 frontmatter 与 kind 一致。
func SeedSkillMD(kind Kind, bundleName, description string) string {
	if description == "" {
		description = "（请填写：这个技能做什么、什么时候该用它）"
	}
	var b strings.Builder
	b.WriteString("---\n")
	if kind == KindAgent {
		// name 必须种进去且等于 bundle 名，否则发布会被 ValidateForPublish 挡下。
		// 让管理员自己想起来写这一行，等于把一个必然踩的坑留给他。
		b.WriteString("name: ")
		b.WriteString(bundleName)
		b.WriteString("\n")
	}
	b.WriteString("description: ")
	b.WriteString(description)
	b.WriteString("\n")
	if kind == KindCommand {
		b.WriteString(disableModelInvocation)
		b.WriteString(": true\n")
	}
	b.WriteString("---\n\n")
	switch kind {
	case KindCommand:
		b.WriteString("在这里写下这个命令要执行的步骤。\n")
	case KindAgent:
		b.WriteString("在这里写下这个 subagent 的职责、工作流程与交付格式。\n")
	default:
		b.WriteString("在这里写下 Claude 应当遵循的指令。\n")
	}
	return b.String()
}
