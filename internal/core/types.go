package core

import "time"

// Kind 是 Bundle 的类型。它决定**落盘形态**（技术方案 §8.2.0）；
// 在同一形态内部，Kind 只决定生成的 frontmatter 与后台展示。
type Kind string

const (
	// KindCommand 生成 disable-model-invocation: true，只有用户敲 /name 才触发。
	KindCommand Kind = "command"
	// KindSkill 不写该字段，Claude 判断相关时可自动加载。
	KindSkill Kind = "skill"
	// KindAgent 是 subagent 定义。它必须落成 agents/<name>.md 这个单文件，
	// 因为 subagent **无法定义在 skill 内部**——只能放在 agents/ 目录下。
	KindAgent Kind = "agent"
)

func (k Kind) Valid() bool { return k == KindCommand || k == KindSkill || k == KindAgent }

// Shape 是落盘形态（技术方案 §8.2.0）。
//
// 它决定目标入口是一个目录还是一个文件，进而决定 ownership 判定、
// 切换方式与回收方式。三条不变量 I1–I3 在两种形态下都成立。
type Shape string

const (
	// ShapeDir 落盘为 skills/<name>/ 目录，入口文件 SKILL.md。
	ShapeDir Shape = "dir"
	// ShapeFile 落盘为 agents/<name>.md 单文件，store 内的入口文件 AGENT.md。
	ShapeFile Shape = "file"
)

// Shape 返回该 Kind 的落盘形态。
func (k Kind) Shape() Shape {
	if k == KindAgent {
		return ShapeFile
	}
	return ShapeDir
}

// EntryFile 返回该形态在 store 版本目录中的入口文件名。
func (s Shape) EntryFile() string {
	if s == ShapeFile {
		return AgentFile
	}
	return SkillFile
}

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
// 刻意不含 "role"：users.role 只有 owner/admin/member，把它当授权维度
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
// 与 Principal.Role（owner/admin/member）正交：后者管「能不能进管理后台」，
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
	Role       string // owner | admin | member
	Suspended  bool
	ProjectIDs []string
}
