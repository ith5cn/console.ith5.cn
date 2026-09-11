// Package projects 负责项目、项目成员与工作区绑定。
//
// 绑定（Binding）是「某人在某台设备的某个目录绑定了哪几个项目」，是同步的主体；
// 设计见 docs/设计-同步协议.md §2。
package projects

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ith5/ith5/internal/audit"
	"github.com/ith5/ith5/internal/organizations"
)

// Project 是逻辑项目，可属于一个团队。
type Project struct {
	ID        string
	OrgID     string
	TeamID    string
	Name      string
	Slug      string
	Archived  bool
	CreatedAt time.Time
	Members   []Member
}

// Member 是项目成员。
type Member struct {
	UserID string
	Email  string
	Name   string
	Role   organizations.MemberRole
}

// BindingState 是绑定状态。
type BindingState string

const (
	BindingActive    BindingState = "active"
	BindingSuspended BindingState = "suspended"
	BindingRevoked   BindingState = "revoked"
)

// Binding 是设备上一个工作目录到若干项目的绑定。只存目录哈希，不存绝对路径。
type Binding struct {
	ID              string
	OrgID           string
	UserID          string
	MachineID       string
	WorkspaceID     string
	DisplayName     string
	ProjectIDs      []string
	AppliedRevision string
	State           BindingState
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastSyncAt      *time.Time
	// ETag 每次写入变化，供 If-Match。
	ETag string
}

var (
	ErrNotFound     = errors.New("projects: 记录不存在")
	ErrSlugTaken    = errors.New("projects: slug 已被占用")
	ErrInvalidInput = errors.New("projects: 输入非法")
	ErrForbidden    = errors.New("projects: 无权操作")
	ErrArchived     = errors.New("projects: 项目已归档")
	ErrETagMismatch = errors.New("projects: 对象已被修改")
)

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// Store 是项目模块的持久化能力。
type Store interface {
	ListProjects(ctx context.Context, orgID string) ([]Project, error)
	GetProject(ctx context.Context, orgID, projectID string) (Project, error)
	CreateProject(ctx context.Context, p Project) (string, error)
	UpdateProject(ctx context.Context, orgID, projectID, name, teamID string, archived bool) error
	PutProjectMember(ctx context.Context, orgID, projectID, userID string, role organizations.MemberRole) error
	DeleteProjectMember(ctx context.Context, orgID, projectID, userID string) error

	CreateBinding(ctx context.Context, b Binding) (Binding, error)
	GetBinding(ctx context.Context, orgID, bindingID string) (Binding, error)
	ListBindings(ctx context.Context, orgID, userID string) ([]Binding, error)
	// UpdateBinding 带 ETag 条件更新；不匹配返回 ErrETagMismatch。
	UpdateBinding(ctx context.Context, b Binding, ifMatch string) (Binding, error)
	SetBindingState(ctx context.Context, orgID, bindingID string, state BindingState) error
}

// Service 编排项目与绑定的写操作。
type Service struct {
	store Store
	audit audit.Recorder
}

// NewService 构造服务。
func NewService(store Store, rec audit.Recorder) *Service {
	return &Service{store: store, audit: rec}
}

// Store 暴露仓储。
func (s *Service) Store() Store { return s.store }

// Actor 是发起操作的人。
type Actor struct {
	UserID    string
	OrgID     string
	MachineID string
	RequestID string
	Subject   organizations.Subject
}

// ---------------------------------------------------------------
// 项目
// ---------------------------------------------------------------

// Create 创建项目。挂在团队下需要团队管理权限，否则需要组织管理权限。
// 创建者自动成为项目 admin。
func (s *Service) Create(ctx context.Context, a Actor, name, slug, teamID string) (string, error) {
	scope := organizations.OrgScope(a.OrgID)
	if teamID != "" {
		scope = organizations.TeamScope(teamID)
	}
	if !a.Subject.Can(organizations.PermMembershipManage, scope) {
		return "", ErrForbidden
	}
	name, slug = strings.TrimSpace(name), strings.TrimSpace(slug)
	if slug == "" || len(slug) > 64 || !slugRe.MatchString(slug) {
		return "", fmt.Errorf("%w: slug 必须是小写字母、数字与连字符", ErrInvalidInput)
	}
	if name == "" || len(name) > 100 {
		return "", fmt.Errorf("%w: 项目名长度 1 到 100", ErrInvalidInput)
	}
	id, err := s.store.CreateProject(ctx, Project{OrgID: a.OrgID, TeamID: teamID, Name: name, Slug: slug})
	if err != nil {
		return "", err
	}
	if err := s.store.PutProjectMember(ctx, a.OrgID, id, a.UserID, organizations.MemberRoleAdmin); err != nil {
		return "", err
	}
	return id, s.record(ctx, a, "project.create", "project", id, map[string]any{"slug": slug, "team_id": teamID})
}

// Update 改名、换团队、归档或恢复。
func (s *Service) Update(ctx context.Context, a Actor, projectID, name, teamID string, archived bool) error {
	if !a.Subject.Can(organizations.PermMembershipManage, organizations.ProjectScope(projectID)) {
		return ErrForbidden
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return fmt.Errorf("%w: 项目名长度 1 到 100", ErrInvalidInput)
	}
	if err := s.store.UpdateProject(ctx, a.OrgID, projectID, name, teamID, archived); err != nil {
		return err
	}
	return s.record(ctx, a, "project.update", "project", projectID, map[string]any{"name": name, "team_id": teamID, "archived": archived})
}

// PutMember 加入项目或改角色。
func (s *Service) PutMember(ctx context.Context, a Actor, projectID, userID string, role organizations.MemberRole) error {
	if !a.Subject.Can(organizations.PermMembershipManage, organizations.ProjectScope(projectID)) {
		return ErrForbidden
	}
	if !role.Valid() {
		return fmt.Errorf("%w: 角色只能是 admin 或 member", ErrInvalidInput)
	}
	p, err := s.store.GetProject(ctx, a.OrgID, projectID)
	if err != nil {
		return err
	}
	if p.Archived {
		return ErrArchived
	}
	if err := s.store.PutProjectMember(ctx, a.OrgID, projectID, userID, role); err != nil {
		return err
	}
	return s.record(ctx, a, "project.member.put", "project", projectID, map[string]any{"user_id": userID, "role": role})
}

// DeleteMember 移出项目。
func (s *Service) DeleteMember(ctx context.Context, a Actor, projectID, userID string) error {
	if !a.Subject.Can(organizations.PermMembershipManage, organizations.ProjectScope(projectID)) {
		return ErrForbidden
	}
	if err := s.store.DeleteProjectMember(ctx, a.OrgID, projectID, userID); err != nil {
		return err
	}
	return s.record(ctx, a, "project.member.delete", "project", projectID, map[string]any{"user_id": userID})
}

// ---------------------------------------------------------------
// 绑定
// ---------------------------------------------------------------

// Bind 创建绑定。调用方必须持设备令牌，且对每个项目有读权限。
func (s *Service) Bind(ctx context.Context, a Actor, workspaceID, displayName string, projectIDs []string) (Binding, error) {
	if a.MachineID == "" {
		return Binding{}, fmt.Errorf("%w: 绑定需要设备令牌", ErrForbidden)
	}
	if err := s.checkProjects(ctx, a, projectIDs); err != nil {
		return Binding{}, err
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" || len(workspaceID) > 128 {
		return Binding{}, fmt.Errorf("%w: workspace_id 必填且不超过 128 字符", ErrInvalidInput)
	}
	b, err := s.store.CreateBinding(ctx, Binding{
		OrgID: a.OrgID, UserID: a.UserID, MachineID: a.MachineID,
		WorkspaceID: workspaceID, DisplayName: strings.TrimSpace(displayName),
		ProjectIDs: dedupe(projectIDs), State: BindingActive,
	})
	if err != nil {
		return Binding{}, err
	}
	return b, s.record(ctx, a, "binding.create", "binding", b.ID, map[string]any{"project_ids": b.ProjectIDs})
}

// Rebind 修改绑定的项目集或显示名，需 If-Match。
func (s *Service) Rebind(ctx context.Context, a Actor, bindingID, ifMatch, displayName string, projectIDs []string) (Binding, error) {
	b, err := s.store.GetBinding(ctx, a.OrgID, bindingID)
	if err != nil {
		return Binding{}, err
	}
	if b.UserID != a.UserID {
		return Binding{}, ErrForbidden
	}
	if b.State == BindingRevoked {
		return Binding{}, fmt.Errorf("%w: 绑定已撤销", ErrForbidden)
	}
	if err := s.checkProjects(ctx, a, projectIDs); err != nil {
		return Binding{}, err
	}
	b.DisplayName = strings.TrimSpace(displayName)
	b.ProjectIDs = dedupe(projectIDs)
	nb, err := s.store.UpdateBinding(ctx, b, ifMatch)
	if err != nil {
		return Binding{}, err
	}
	return nb, s.record(ctx, a, "binding.update", "binding", nb.ID, map[string]any{"project_ids": nb.ProjectIDs})
}

// Unbind 撤销自己的绑定。
func (s *Service) Unbind(ctx context.Context, a Actor, bindingID string) error {
	b, err := s.store.GetBinding(ctx, a.OrgID, bindingID)
	if err != nil {
		return err
	}
	if b.UserID != a.UserID && !a.Subject.Can(organizations.PermDeviceManage, organizations.OrgScope(a.OrgID)) {
		return ErrForbidden
	}
	if err := s.store.SetBindingState(ctx, a.OrgID, bindingID, BindingRevoked); err != nil {
		return err
	}
	return s.record(ctx, a, "binding.revoke", "binding", bindingID, nil)
}

// checkProjects 校验每个项目存在、未归档、且调用方有读权限。
func (s *Service) checkProjects(ctx context.Context, a Actor, projectIDs []string) error {
	if len(projectIDs) == 0 {
		return fmt.Errorf("%w: 至少绑定一个项目", ErrInvalidInput)
	}
	if len(projectIDs) > 20 {
		return fmt.Errorf("%w: 一个绑定最多 20 个项目", ErrInvalidInput)
	}
	for _, pid := range projectIDs {
		p, err := s.store.GetProject(ctx, a.OrgID, pid)
		if err != nil {
			return fmt.Errorf("%w: 项目 %s 不存在", ErrInvalidInput, pid)
		}
		if p.Archived {
			return fmt.Errorf("%w: 项目 %s 已归档", ErrArchived, p.Slug)
		}
		if !a.Subject.Can(organizations.PermProjectRead, organizations.ProjectScope(pid)) {
			return fmt.Errorf("%w: 不是项目 %s 的成员", ErrForbidden, p.Slug)
		}
	}
	return nil
}

func (s *Service) record(ctx context.Context, a Actor, typ, targetType, targetID string, detail map[string]any) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, audit.Event{
		OrgID: a.OrgID, ActorUserID: a.UserID, Type: typ,
		TargetType: targetType, TargetID: targetID, Detail: detail, RequestID: a.RequestID,
	})
}

func dedupe(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
