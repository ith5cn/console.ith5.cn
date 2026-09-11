// Package organizations 负责组织结构：组织、策略、团队、成员、reviewer，以及由三层角色推导权限。
//
// 设计见 docs/设计-身份与组织模型.md §2–§3。角色只有三层（组织 / 团队 / 项目），
// 细粒度权限从角色推导，不单独存储。
package organizations

import (
	"context"
	"errors"
	"time"

	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/resources"
)

// Organization 是租户。
type Organization struct {
	ID        string
	Name      string
	Slug      string
	CreatedAt time.Time
}

// Policy 是组织级策略（org_policies）。
type Policy struct {
	OrgID             string
	RequiredApprovals int
	LearningsReview   bool
	ConfidencePrune   float64
	ConfidencePromote float64
	RetentionMonths   int
}

// DefaultPolicy 是新组织的默认值，与建表默认一致。
func DefaultPolicy(orgID string) Policy {
	return Policy{OrgID: orgID, RequiredApprovals: 1, ConfidencePrune: 0.15, ConfidencePromote: 0.7, RetentionMonths: 13}
}

// Validate 检查策略取值。
func (p Policy) Validate() error {
	switch {
	case p.RequiredApprovals < 1 || p.RequiredApprovals > 10:
		return errors.New("required_approvals 必须在 1 到 10 之间")
	case p.ConfidencePrune < 0 || p.ConfidencePrune > 1 || p.ConfidencePromote < 0 || p.ConfidencePromote > 1:
		return errors.New("置信度阈值必须在 0 到 1 之间")
	case p.ConfidencePrune >= p.ConfidencePromote:
		return errors.New("归档阈值必须小于晋升阈值")
	case p.RetentionMonths < 1 || p.RetentionMonths > 120:
		return errors.New("retention_months 必须在 1 到 120 之间")
	}
	return nil
}

// MemberRole 是团队与项目内的角色。
type MemberRole string

const (
	MemberRoleAdmin  MemberRole = "admin"
	MemberRoleMember MemberRole = "member"
)

// Valid 判断角色合法。
func (r MemberRole) Valid() bool { return r == MemberRoleAdmin || r == MemberRoleMember }

// Team 是组织下的团队。
type Team struct {
	ID        string
	OrgID     string
	Name      string
	Slug      string
	Archived  bool
	CreatedAt time.Time
	// Members 只在详情读取时填充。
	Members []TeamMember
}

// TeamMember 是团队成员。
type TeamMember struct {
	UserID string
	Email  string
	Name   string
	Role   MemberRole
}

// Member 是组织成员的后台视图。
type Member struct {
	UserID     string
	AccountID  string
	Email      string
	Name       string
	Role       identity.Role
	Suspended  bool
	Machines   int
	LastSeenAt *time.Time
	CreatedAt  time.Time
}

// Reviewer 是某团队或项目上额外指定的审核人。
type Reviewer struct {
	Level   resources.Level
	OwnerID string
	UserID  string
	Email   string
}

var (
	ErrNotFound      = errors.New("organizations: 记录不存在")
	ErrSlugTaken     = errors.New("organizations: slug 已被占用")
	ErrEmailTaken    = errors.New("organizations: 邮箱已是成员")
	ErrLastOwner     = errors.New("organizations: 不能停用或降级最后一个 owner")
	ErrInvalidInput  = errors.New("organizations: 输入非法")
	ErrReviewerLevel = errors.New("organizations: reviewer 只能指定在团队或项目上")
)

// Store 是组织模块的持久化能力。
type Store interface {
	GetOrganization(ctx context.Context, orgID string) (Organization, error)
	// CreateOrganization 建组织，并把 accountID 作为 owner 加入；返回组织与新成员 userID。
	CreateOrganization(ctx context.Context, accountID, name, slug string) (Organization, string, error)
	GetOrganizationBySlug(ctx context.Context, slug string) (Organization, error)
	UpdateOrganization(ctx context.Context, orgID, name string) error
	GetPolicy(ctx context.Context, orgID string) (Policy, error)
	PutPolicy(ctx context.Context, p Policy) error

	ListTeams(ctx context.Context, orgID string) ([]Team, error)
	GetTeam(ctx context.Context, orgID, teamID string) (Team, error)
	CreateTeam(ctx context.Context, t Team) (string, error)
	UpdateTeam(ctx context.Context, orgID, teamID, name string, archived bool) error
	PutTeamMember(ctx context.Context, orgID, teamID, userID string, role MemberRole) error
	DeleteTeamMember(ctx context.Context, orgID, teamID, userID string) error

	ListMembers(ctx context.Context, orgID string) ([]Member, error)
	GetMember(ctx context.Context, orgID, userID string) (Member, error)
	// CreateLocalMember 创建 local 账号（不存在时）与成员记录；返回 userID。
	CreateLocalMember(ctx context.Context, orgID, email, name string, role identity.Role, pwHash string) (string, error)
	SetMemberRole(ctx context.Context, orgID, userID string, role identity.Role) error
	CountOwners(ctx context.Context, orgID string) (int, error)
	SetPassword(ctx context.Context, orgID, userID, pwHash string) error

	ListReviewers(ctx context.Context, orgID string, level resources.Level, ownerID string) ([]Reviewer, error)
	PutReviewers(ctx context.Context, orgID string, level resources.Level, ownerID string, userIDs []string) error

	// LoadSubject 一次性读出某成员的全部角色关系，供权限推导。
	LoadSubject(ctx context.Context, userID string) (Subject, error)
}
