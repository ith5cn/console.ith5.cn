// Package resources 承载内容模型的纯逻辑：资源类型、路径安全、校验和、层级与授权解析。
//
// 约束：
//   - 零第三方依赖。这些逻辑「错了会出安全问题」，必须能被完整审计。
//   - 不访问网络、数据库或文件系统。所有外部状态由调用方以参数传入。
//
// 设计见 docs/设计-资源类型与层级.md。
package resources

import (
	"errors"
	"strings"
)

// ErrNotFound 表示资源或版本不存在，由仓储返回。
var ErrNotFound = errors.New("resources: 记录不存在")

// Kind 是资源类型（docs/设计-资源类型与层级.md §2）。
type Kind string

const (
	// KindSkill 是目录形态，入口 SKILL.md，其余文件不限。
	KindSkill Kind = "skill"
	// KindRule 是一份 Markdown 规范，合并进各工具的规则目录。
	KindRule Kind = "rule"
	// KindDoc 是一份文档，name 即相对路径，允许子目录。
	KindDoc Kind = "doc"
	// KindAgent 是 subagent 定义，正本是 teamai 的 YAML，下发时按工具渲染。
	KindAgent Kind = "agent"
	// KindHook 是一条 hook 声明，可附带 scripts/ 下的脚本文件。
	KindHook Kind = "hook"
	// KindMCP 是一个 MCP server 定义。
	KindMCP Kind = "mcp"
	// KindEnv 是一个环境变量，name 即 KEY。
	KindEnv Kind = "env"
	// KindClaudeMD 是注入 CLAUDE.md / AGENTS.md 的片段。
	KindClaudeMD Kind = "claudemd"
	// KindCulture 是每层最多一份的团队文化，name 固定为 culture。
	KindCulture Kind = "culture"
	// KindPolicy 是每层最多一份的策略块，name 固定为 policy；解析时字段级合并取更严者。
	KindPolicy Kind = "policy"
	// KindLearning 是成员分享的经验文档，走发布面但有独立权限与直接发布通道。
	KindLearning Kind = "learning"
)

// AllKinds 是全部合法类型，供校验与后台下拉框使用。
var AllKinds = []Kind{
	KindSkill, KindRule, KindDoc, KindAgent, KindHook, KindMCP,
	KindEnv, KindClaudeMD, KindCulture, KindPolicy, KindLearning,
}

// Valid 判断类型是否合法。
func (k Kind) Valid() bool {
	for _, x := range AllKinds {
		if k == x {
			return true
		}
	}
	return false
}

// Singleton 表示该类型在每个层级最多一份，name 固定。
func (k Kind) Singleton() bool { return k == KindCulture || k == KindPolicy }

// EntryFile 是该类型必须包含的入口文件名。
//
// 入口用固定名而非资源名，是为了让内容寻址与去重不受资源名影响；
// 渲染成 teamai 仓库视图时才改成目标名。
func (k Kind) EntryFile() string {
	switch k {
	case KindSkill:
		return "SKILL.md"
	case KindRule:
		return "RULE.md"
	case KindDoc:
		return "DOC.md"
	case KindAgent:
		return "AGENT.yaml"
	case KindHook:
		return "HOOK.yaml"
	case KindMCP:
		return "MCP.yaml"
	case KindEnv:
		return "ENV.yaml"
	case KindClaudeMD:
		return "CLAUDEMD.md"
	case KindCulture:
		return "CULTURE.md"
	case KindPolicy:
		return "POLICY.yaml"
	case KindLearning:
		return "LEARNING.md"
	default:
		return ""
	}
}

// AllowsExtraFiles 表示除入口外还可以带其他文件。
//
// skill 是目录形态，天然多文件；hook 允许 scripts/ 下的脚本作为附件。
// 其余类型只有一份内容，多出来的文件在渲染时无处安放，发布口直接拒绝。
func (k Kind) AllowsExtraFiles() bool { return k == KindSkill || k == KindHook }

// Level 是资源的落点层级（§3）。
type Level string

const (
	LevelOrg     Level = "org"
	LevelTeam    Level = "team"
	LevelProject Level = "project"
)

// Valid 判断层级是否合法。
func (l Level) Valid() bool { return l == LevelOrg || l == LevelTeam || l == LevelProject }

// Specificity 越大越具体；解析时同键取更具体的层级。
func (l Level) Specificity() int {
	switch l {
	case LevelProject:
		return 3
	case LevelTeam:
		return 2
	case LevelOrg:
		return 1
	default:
		return 0
	}
}

// Ref 是资源在一份快照内的唯一键：kind/name。
//
// 不能只用 name：同一个名字可以既是 skill 又是 agent，两者都要装。
type Ref string

// MakeRef 构造 kind/name 形式的键。
func MakeRef(kind Kind, name string) Ref { return Ref(string(kind) + "/" + name) }

// Split 拆回 kind 与 name。非法输入返回空 Kind。
func (r Ref) Split() (Kind, string) {
	s := string(r)
	i := strings.IndexByte(s, '/')
	if i < 0 {
		return "", s
	}
	return Kind(s[:i]), s[i+1:]
}

// Kind 返回键的类型部分。
func (r Ref) Kind() Kind { k, _ := r.Split(); return k }

// Name 返回键的名字部分。
func (r Ref) Name() string { _, n := r.Split(); return n }
