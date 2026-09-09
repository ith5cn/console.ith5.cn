package core

import (
	"strings"
	"time"
)

// Kind 是 Bundle 的类型。它决定**落盘形态**与**落盘位置**（技术方案 §8.2.0）；
// 在同一形态内部，Kind 还决定生成的 frontmatter 与后台展示。
type Kind string

const (
	// KindCommand 生成 disable-model-invocation: true，只有用户敲 /name 才触发。
	KindCommand Kind = "command"
	// KindSkill 不写该字段，Claude 判断相关时可自动加载。
	KindSkill Kind = "skill"
	// KindAgent 是 subagent 定义。它必须落成 agents/<name>.md 这个单文件，
	// 因为 subagent **无法定义在 skill 内部**——只能放在 agents/ 目录下。
	KindAgent Kind = "agent"
	// KindHook 是钩子脚本，落成 hooks/<name>.js。
	//
	// 注意：脚本落盘**不等于**钩子生效——Claude Code 只执行 settings.json 的
	// hooks 里登记过的命令。要让它真正跑起来，还需要一个 KindSetting 的
	// bundle 把它登记进去。这两件事故意分开：脚本是内容，登记是配置，
	// 合在一起会让「装了脚本但没登记」和「登记了但脚本没装」这两种
	// 半生效状态无法分别诊断。
	KindHook Kind = "hook"
	// KindWorkflow 是 Workflow 编排脚本，落成 workflows/<name>.js。
	KindWorkflow Kind = "workflow"
	// KindStandard 是团队规范文档，落成 standards/<name>.md。
	//
	// 它不是 Claude Code 的原生概念——没有任何机制会自动加载 standards/。
	// 它靠 CLAUDE.md 或 skill 正文里的引用生效，分发它只是保证「引用得到」。
	KindStandard Kind = "standard"
	// KindSetting 是 settings.json 片段，合并进 <claude_home>/settings.json。
	KindSetting Kind = "setting"
	// KindMCP 是 MCP server 定义，合并进 ~/.claude.json 的 mcpServers。
	KindMCP Kind = "mcp"
)

// AllKinds 是全部合法 Kind，供服务端校验与后台下拉框使用。
// skill 排在 command 前面不是随意的：两者共用 skills/ 目录且落盘完全相同，
// 重建时若拿不到确切 kind（老 marker 没记），要按更常见的那个兜底。
var AllKinds = []Kind{
	KindSkill, KindCommand, KindAgent, KindHook,
	KindWorkflow, KindStandard, KindSetting, KindMCP,
}

func (k Kind) Valid() bool {
	for _, x := range AllKinds {
		if k == x {
			return true
		}
	}
	return false
}

// Shape 是落盘形态（技术方案 §8.2.0）。
//
// 它决定目标入口是一个目录、一个文件，还是用户 JSON 文件里的若干键，
// 进而决定 ownership 判定、切换方式与回收方式。
type Shape string

const (
	// ShapeDir 落盘为 skills/<name>/ 目录，入口文件 SKILL.md。
	ShapeDir Shape = "dir"
	// ShapeFile 落盘为 <root>/<name><ext> 单文件，root 与 ext 由 Kind 决定。
	ShapeFile Shape = "file"
	// ShapeMerge 不占独立入口，而是把内容合并进用户已有的 JSON 配置文件。
	//
	// 这是唯一会写进用户自有文件的形态，因此它的归属判定不能靠指针或
	// marker——只能靠「当前值是否仍等于我方上次写入的值」。判不出是我方的，
	// 一律不碰（详见 cli/merge.go）。
	ShapeMerge Shape = "merge"
)

// Shape 返回该 Kind 的落盘形态。
func (k Kind) Shape() Shape {
	switch k {
	case KindAgent, KindHook, KindWorkflow, KindStandard:
		return ShapeFile
	case KindSetting, KindMCP:
		return ShapeMerge
	default:
		return ShapeDir
	}
}

// Root 返回该 Kind 在 claude_home 下的落盘根目录（相对路径）。
// ShapeMerge 没有独立入口，返回空串。
func (k Kind) Root() string {
	switch k {
	case KindAgent:
		return "agents"
	case KindHook:
		return "hooks"
	case KindWorkflow:
		return "workflows"
	case KindStandard:
		return "standards"
	case KindSetting, KindMCP:
		return ""
	default:
		return "skills"
	}
}

// Ext 返回文件形态的目标扩展名。目录形态与合并形态返回空串。
func (k Kind) Ext() string {
	switch k {
	case KindAgent, KindStandard:
		return ".md"
	case KindHook, KindWorkflow:
		return ".js"
	default:
		return ""
	}
}

// EntryFile 返回该 Kind 在 store 版本目录中的入口文件名。
//
// store 侧用固定名（AGENT.md 而非 <bundle>.md），是为了让内容寻址与
// 去重不受 bundle 名影响；物化时才改成目标名。
func (k Kind) EntryFile() string {
	switch k {
	case KindAgent:
		return AgentFile
	case KindHook:
		return HookFile
	case KindWorkflow:
		return WorkflowFile
	case KindStandard:
		return StandardFile
	case KindSetting:
		return SettingFile
	case KindMCP:
		return MCPFile
	default:
		return SkillFile
	}
}

// Ref 是 bundle 在客户端的唯一键：kind/name。
//
// **不能只用 name**：数据库唯一键是 (org, kind, name)，同一个名字可以既是
// skill 又是 agent（ith5-code-reviewer 就是如此——一个是操作手册，一个是
// subagent 定义，两者都要装）。lock、ownership 与 Plan 若以 name 为键，
// 这两条会互相覆盖，表现为每次 sync 都在反复改写同一个入口。
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

func (r Ref) Kind() Kind   { k, _ := r.Split(); return k }
func (r Ref) Name() string { _, n := r.Split(); return n }

// Scope 是 Bundle 的授权范围，**不是安装位置**（PRD §9.4）。
// 所有 Bundle 一律安装到用户级 ~/.claude/ 下（skills/ 或 agents/，按形态）。
type Scope string

const (
	ScopeEnterprise Scope = "enterprise"
	ScopeProject    Scope = "project"
)

// File 是 Bundle 版本中的一个文件。Path 必须是相对路径。
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// BundleMeta 是 manifest 中的一条，不含正文。
type BundleMeta struct {
	ID          string `json:"id"`
	OrgID       string `json:"-"`
	Name        string `json:"name"`
	Kind        Kind   `json:"kind"`
	Scope       Scope  `json:"-"`
	ProjectID   string `json:"-"`
	Archived    bool   `json:"-"`
	Version     int    `json:"version"`
	Checksum    string `json:"checksum"`
	Description string `json:"description"`
}

// SubjectType 是 Assignment 的授权主体类型。
//
// 刻意不含 "role"：users.role 只有 owner/admin/viewer/member，把它当授权维度
// 既无实际用途，又会被误读成「岗位」——那是 PermissionGroup 的职责。
type SubjectType string

const (
	SubjectOrg     SubjectType = "org"
	SubjectUser    SubjectType = "user"
	SubjectProject SubjectType = "project"
)

// PermissionGroup 是一组 skill/command 的具名集合（classic RBAC 里的 role）。
// 管理员维护「后端工具包」这样的组，再把组授权给人。
//
// 与 Principal.Role（owner/admin/viewer/member）正交：后者管「能不能进管理后台」，
// 前者管「能拿到哪些内容」。
type PermissionGroup struct {
	ID        string
	OrgID     string
	Key       string
	Name      string
	Archived  bool
	BundleIDs []string // 多对多：一个 bundle 可属于多个组
}

// Assignment 是一条授权。
//
// 授权目标二选一：BundleID（一次性/临时授权）或 GroupID（常规路径）。
// ExpiresAt 为 nil 表示永久（PRD C5）。
type Assignment struct {
	OrgID       string
	BundleID    string // 与 GroupID 二选一
	GroupID     string // 与 BundleID 二选一
	SubjectType SubjectType
	SubjectID   string // SubjectOrg 时为空
	ExpiresAt   *time.Time
}

// GrantSource 说明某个 Bundle 是「凭什么」拿到的。
//
// 管理员最常问的两个问题——「他为什么能拿到这个」和「我授权了他为什么没有」
// ——没有这个结构就答不了。
type GrantSource struct {
	SubjectType SubjectType
	SubjectID   string
	// 非空表示经由权限组授予，可向管理员展示
	// 「你拿到 corp-db-review，是因为它在「后端工具包」里」。
	GroupID   string
	GroupName string
}

// Grant 是解析结果的一项：一个 Bundle 加上它的全部授权来源。
type Grant struct {
	Bundle BundleMeta
	Via    []GrantSource
}

// Principal 是发起请求的人及其项目归属。
type Principal struct {
	UserID     string
	OrgID      string
	Role       string // owner | admin | viewer | member
	Suspended  bool
	ProjectIDs []string
}
