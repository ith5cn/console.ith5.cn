package organizations

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ith5/ith5/internal/audit"
	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/resources"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// Service 编排组织结构的读写，并在每个写操作后记审计。
type Service struct {
	store Store
	audit audit.Recorder
}

// NewService 构造服务。
func NewService(store Store, rec audit.Recorder) *Service {
	return &Service{store: store, audit: rec}
}

// Store 暴露仓储，供 api 层做只读查询与权限加载。
func (s *Service) Store() Store { return s.store }

// Actor 是发起写操作的人，用于权限判断与审计。
type Actor struct {
	UserID    string
	OrgID     string
	RequestID string
	Subject   Subject
}

func (a Actor) can(p Permission, sc Scope) error {
	if !a.Subject.Can(p, sc) {
		return ErrForbidden
	}
	return nil
}

// ErrForbidden 表示主体没有该权限。api 层映射为 403。
var ErrForbidden = errors.New("organizations: 无权操作")

// ---------------------------------------------------------------
// 组织与策略
// ---------------------------------------------------------------

// Rename 修改组织名。
// CreateOrganization 让任何账号自建组织并成为 owner（#341 管理员旅程的第一步）。
// 不需要既有组织的权限：这是账号级动作，审计记在新组织名下。
func (s *Service) CreateOrganization(ctx context.Context, accountID, name, slug string) (Organization, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return Organization{}, "", fmt.Errorf("%w: 名称长度 1 到 100", ErrInvalidInput)
	}
	if err := validateSlug(slug); err != nil {
		return Organization{}, "", err
	}
	o, userID, err := s.store.CreateOrganization(ctx, accountID, name, slug)
	if err != nil {
		return Organization{}, "", err
	}
	_ = s.record(ctx, Actor{UserID: userID, OrgID: o.ID}, "organization.create", "organization", o.ID, map[string]any{"slug": slug})
	return o, userID, nil
}

func (s *Service) Rename(ctx context.Context, a Actor, name string) error {
	if err := a.can(PermOrganizationManage, OrgScope(a.OrgID)); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return fmt.Errorf("%w: 组织名长度 1 到 100", ErrInvalidInput)
	}
	if err := s.store.UpdateOrganization(ctx, a.OrgID, name); err != nil {
		return err
	}
	return s.record(ctx, a, "organization.rename", "organization", a.OrgID, map[string]any{"name": name})
}

// GetPolicy 读取策略；没有记录时返回默认值。
func (s *Service) GetPolicy(ctx context.Context, orgID string) (Policy, error) {
	p, err := s.store.GetPolicy(ctx, orgID)
	if errors.Is(err, ErrNotFound) {
		return DefaultPolicy(orgID), nil
	}
	return p, err
}

// PutPolicy 覆盖策略。
func (s *Service) PutPolicy(ctx context.Context, a Actor, p Policy) error {
	if err := a.can(PermOrganizationManage, OrgScope(a.OrgID)); err != nil {
		return err
	}
	p.OrgID = a.OrgID
	if err := p.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if err := s.store.PutPolicy(ctx, p); err != nil {
		return err
	}
	return s.record(ctx, a, "organization.policy", "organization", a.OrgID, map[string]any{
		"required_approvals": p.RequiredApprovals, "learnings_review": p.LearningsReview,
	})
}

// ---------------------------------------------------------------
// 团队
// ---------------------------------------------------------------

// CreateTeam 创建团队；创建者自动成为团队 admin，否则新团队没人能管。
func (s *Service) CreateTeam(ctx context.Context, a Actor, name, slug string) (string, error) {
	if err := a.can(PermMembershipManage, OrgScope(a.OrgID)); err != nil {
		return "", err
	}
	name, slug = strings.TrimSpace(name), strings.TrimSpace(slug)
	if err := validateSlug(slug); err != nil {
		return "", err
	}
	if name == "" || len(name) > 100 {
		return "", fmt.Errorf("%w: 团队名长度 1 到 100", ErrInvalidInput)
	}
	id, err := s.store.CreateTeam(ctx, Team{OrgID: a.OrgID, Name: name, Slug: slug})
	if err != nil {
		return "", err
	}
	if err := s.store.PutTeamMember(ctx, a.OrgID, id, a.UserID, MemberRoleAdmin); err != nil {
		return "", err
	}
	return id, s.record(ctx, a, "team.create", "team", id, map[string]any{"slug": slug})
}

// UpdateTeam 改名或归档。
func (s *Service) UpdateTeam(ctx context.Context, a Actor, teamID, name string, archived bool) error {
	if err := a.can(PermMembershipManage, TeamScope(teamID)); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return fmt.Errorf("%w: 团队名长度 1 到 100", ErrInvalidInput)
	}
	if err := s.store.UpdateTeam(ctx, a.OrgID, teamID, name, archived); err != nil {
		return err
	}
	return s.record(ctx, a, "team.update", "team", teamID, map[string]any{"name": name, "archived": archived})
}

// PutTeamMember 加入或改角色。目标必须是本组织成员。
func (s *Service) PutTeamMember(ctx context.Context, a Actor, teamID, userID string, role MemberRole) error {
	if err := a.can(PermMembershipManage, TeamScope(teamID)); err != nil {
		return err
	}
	if !role.Valid() {
		return fmt.Errorf("%w: 角色只能是 admin 或 member", ErrInvalidInput)
	}
	if _, err := s.store.GetMember(ctx, a.OrgID, userID); err != nil {
		return err
	}
	if err := s.store.PutTeamMember(ctx, a.OrgID, teamID, userID, role); err != nil {
		return err
	}
	return s.record(ctx, a, "team.member.put", "team", teamID, map[string]any{"user_id": userID, "role": role})
}

// DeleteTeamMember 移出团队。
func (s *Service) DeleteTeamMember(ctx context.Context, a Actor, teamID, userID string) error {
	if err := a.can(PermMembershipManage, TeamScope(teamID)); err != nil {
		return err
	}
	if err := s.store.DeleteTeamMember(ctx, a.OrgID, teamID, userID); err != nil {
		return err
	}
	return s.record(ctx, a, "team.member.delete", "team", teamID, map[string]any{"user_id": userID})
}

// ---------------------------------------------------------------
// 组织成员
// ---------------------------------------------------------------

// CreateLocalMember 邀请一个密码账号进组织。
func (s *Service) CreateLocalMember(ctx context.Context, a Actor, email, name string, role identity.Role, pwHash string) (string, error) {
	if err := a.can(PermMembershipManage, OrgScope(a.OrgID)); err != nil {
		return "", err
	}
	if role == identity.RoleOwner && a.Subject.OrgRole != identity.RoleOwner {
		return "", ErrForbidden // 只有 owner 能造 owner
	}
	email = identity.NormalizeEmail(email)
	if !strings.Contains(email, "@") || len(email) > 254 {
		return "", fmt.Errorf("%w: 邮箱格式非法", ErrInvalidInput)
	}
	if !validRole(role) {
		return "", fmt.Errorf("%w: 角色非法", ErrInvalidInput)
	}
	id, err := s.store.CreateLocalMember(ctx, a.OrgID, email, strings.TrimSpace(name), role, pwHash)
	if err != nil {
		return "", err
	}
	return id, s.record(ctx, a, "member.create", "user", id, map[string]any{"email": email, "role": role})
}

// SetMemberRole 改组织角色。不能把最后一个 owner 降级；只有 owner 能授予或收回 owner。
func (s *Service) SetMemberRole(ctx context.Context, a Actor, userID string, role identity.Role) error {
	if err := a.can(PermMembershipManage, OrgScope(a.OrgID)); err != nil {
		return err
	}
	if !validRole(role) {
		return fmt.Errorf("%w: 角色非法", ErrInvalidInput)
	}
	cur, err := s.store.GetMember(ctx, a.OrgID, userID)
	if err != nil {
		return err
	}
	if (role == identity.RoleOwner || cur.Role == identity.RoleOwner) && a.Subject.OrgRole != identity.RoleOwner {
		return ErrForbidden
	}
	if cur.Role == identity.RoleOwner && role != identity.RoleOwner {
		n, err := s.store.CountOwners(ctx, a.OrgID)
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastOwner
		}
	}
	if err := s.store.SetMemberRole(ctx, a.OrgID, userID, role); err != nil {
		return err
	}
	return s.record(ctx, a, "member.role", "user", userID, map[string]any{"role": role})
}

// ResetPassword 由管理员重置密码。
func (s *Service) ResetPassword(ctx context.Context, a Actor, userID, pwHash string) error {
	if err := a.can(PermMembershipManage, OrgScope(a.OrgID)); err != nil {
		return err
	}
	if err := s.store.SetPassword(ctx, a.OrgID, userID, pwHash); err != nil {
		return err
	}
	return s.record(ctx, a, "member.password", "user", userID, nil)
}

// ---------------------------------------------------------------
// reviewer
// ---------------------------------------------------------------

// PutReviewers 覆盖某团队或项目的额外 reviewer 名单。
func (s *Service) PutReviewers(ctx context.Context, a Actor, sc Scope, userIDs []string) error {
	if sc.Level == resources.LevelOrg {
		return ErrReviewerLevel
	}
	if err := a.can(PermMembershipManage, sc); err != nil {
		return err
	}
	for _, uid := range userIDs {
		if _, err := s.store.GetMember(ctx, a.OrgID, uid); err != nil {
			return fmt.Errorf("%w: reviewer %s 不是本组织成员", ErrInvalidInput, uid)
		}
	}
	if err := s.store.PutReviewers(ctx, a.OrgID, sc.Level, sc.OwnerID, userIDs); err != nil {
		return err
	}
	return s.record(ctx, a, "reviewers.put", string(sc.Level), sc.OwnerID, map[string]any{"user_ids": userIDs})
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

func validateSlug(slug string) error {
	if slug == "" || len(slug) > 64 || !slugRe.MatchString(slug) {
		return fmt.Errorf("%w: slug 必须是小写字母、数字与连字符", ErrInvalidInput)
	}
	return nil
}

func validRole(r identity.Role) bool {
	switch r {
	case identity.RoleOwner, identity.RoleAdmin, identity.RoleMember, identity.RoleViewer:
		return true
	}
	return false
}
