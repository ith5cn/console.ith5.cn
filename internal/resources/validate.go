package resources

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNoFrontmatter     = errors.New("入口文件缺少 frontmatter")
	ErrBadFrontmatter    = errors.New("frontmatter 未闭合")
	ErrNoDescription     = errors.New("缺少 description")
	ErrAgentNameMismatch = errors.New("agent 的 name 与资源名不一致")
	ErrMissingField      = errors.New("缺少必填字段")
	ErrEmptyContent      = errors.New("内容不能为空")
	// ErrSecretValueOnServer：标记为 secret 的变量不能把值放到服务端。
	ErrSecretValueOnServer = errors.New("secret 变量不能带 value，值只在本机设置")
	// ErrEnvNeedsSecretFlag：名字像凭据的变量必须标记 secret: true。
	ErrEnvNeedsSecretFlag = errors.New("疑似凭据的变量必须标记 secret: true，值只在本机保存")
)

// Frontmatter 是我们关心的那几个顶层字段。
//
// 刻意不引 YAML 库：本包保持零依赖，而我们只需要判断少数顶层标量是否存在。
type Frontmatter struct {
	Name        string
	Description string
	Tags        []string
}

// ParseFrontmatter 从 Markdown 正文解析 frontmatter。
//
// 只处理顶层的 `key: value`。嵌套结构、多行字符串一律忽略——不需要理解它们，只需要不误判。
func ParseFrontmatter(content string) (Frontmatter, error) {
	var fm Frontmatter
	s := strings.TrimLeft(content, "\uFEFF \t\r\n")
	if !strings.HasPrefix(s, "---") {
		return fm, ErrNoFrontmatter
	}
	rest := s[3:]
	i := strings.IndexByte(rest, '\n')
	if i < 0 {
		return fm, ErrBadFrontmatter
	}
	rest = rest[i+1:]
	var block string
	if strings.HasPrefix(rest, "---") {
		block = ""
	} else {
		end := strings.Index(rest, "\n---")
		if end < 0 {
			return fm, ErrBadFrontmatter
		}
		block = rest[:end]
	}
	for key, val := range topLevelScalars(block) {
		switch key {
		case "name":
			fm.Name = val
		case "description":
			fm.Description = val
		case "tags":
			fm.Tags = parseInlineList(val)
		}
	}
	return fm, nil
}

// topLevelScalars 提取 YAML 文本里顶层的 `key: value` 标量对。
// 缩进行、注释、无冒号的行都跳过。值去掉包裹引号。
func topLevelScalars(block string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") ||
			strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(val), `"'`)
	}
	return out
}

// topLevelKeys 提取 YAML 文本里全部顶层键，包括值为块（`key:` 后换行缩进）的键。
func topLevelKeys(block string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") ||
			strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		key, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = true
	}
	return out
}

func parseInlineList(val string) []string {
	val = strings.TrimSpace(val)
	if !strings.HasPrefix(val, "[") || !strings.HasSuffix(val, "]") {
		return nil
	}
	var out []string
	for _, part := range strings.Split(val[1:len(val)-1], ",") {
		if p := strings.Trim(strings.TrimSpace(part), `"'`); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ValidateForPublish 在发布口校验内容与声明的一致性。
//
// 存在的理由：kind 只是后台的标签，各 AI 工具只认文件内容。
// 内容与类型不一致的资源「发布成功、用起来却没有」，这类静默失效只能挡在发布口。
func ValidateForPublish(kind Kind, name string, files []File) error {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	if err := ValidateFiles(kind, paths); err != nil {
		return err
	}
	if err := ValidateName(kind, name); err != nil {
		return err
	}
	entry := kind.EntryFile()
	var content string
	for _, f := range files {
		if f.Path == entry {
			content = f.Content
			break
		}
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("%s: %w", entry, ErrEmptyContent)
	}

	switch kind {
	case KindSkill, KindLearning:
		fm, err := ParseFrontmatter(content)
		if err != nil {
			return fmt.Errorf("%s: %w（至少需要 ---\\ndescription: ...\\n---）", entry, err)
		}
		if fm.Description == "" && kind == KindSkill {
			return fmt.Errorf("%s: %w", entry, ErrNoDescription)
		}
		if kind == KindLearning && fm.Name == "" && !strings.Contains(content, "title:") {
			return fmt.Errorf("%s: %w: title", entry, ErrMissingField)
		}
	case KindAgent:
		// teamai 的 agent YAML：name 必须等于资源名，否则派发时找不到；
		// description 与 instructions 是各工具渲染的最少字段。
		scalars := topLevelScalars(content)
		keys := topLevelKeys(content)
		if scalars["name"] == "" {
			return fmt.Errorf("%s: %w: name", entry, ErrMissingField)
		}
		if scalars["name"] != name {
			return fmt.Errorf("%w: 文件写的是 %q，资源名是 %q", ErrAgentNameMismatch, scalars["name"], name)
		}
		for _, k := range []string{"description", "instructions"} {
			if !keys[k] {
				return fmt.Errorf("%s: %w: %s", entry, ErrMissingField, k)
			}
		}
	case KindHook:
		keys := topLevelKeys(content)
		for _, k := range []string{"id", "event", "command"} {
			if !keys[k] {
				return fmt.Errorf("%s: %w: %s", entry, ErrMissingField, k)
			}
		}
	case KindMCP:
		keys := topLevelKeys(content)
		if !keys["transport"] {
			return fmt.Errorf("%s: %w: transport", entry, ErrMissingField)
		}
	case KindEnv:
		keys := topLevelKeys(content)
		scalars := topLevelScalars(content)
		secret := scalars["secret"] == "true"
		switch {
		case secret && strings.TrimSpace(scalars["value"]) != "":
			return fmt.Errorf("%s: %w", entry, ErrSecretValueOnServer)
		case !secret && !keys["value"]:
			return fmt.Errorf("%s: %w: value（或标记 secret: true）", entry, ErrMissingField)
		case !secret && secretLikeEnvName.MatchString(name):
			return fmt.Errorf("%s: %w", entry, ErrEnvNeedsSecretFlag)
		}
	}
	// 所有文本文件过一遍密钥扫描：env 与 secret 分离之后，服务端上就不该再出现凭据
	return ScanFiles(files)
}

// IsSecretEnv 判断一份 ENV.yaml 是否标记为 secret。
func IsSecretEnv(content string) bool { return topLevelScalars(content)["secret"] == "true" }
