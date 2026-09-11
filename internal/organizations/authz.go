package organizations

import (
	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/resources"
)

// Permission 是 #341 要求的细粒度权限。取值与设计文档 §3 的推导表一一对应。
type Permission string

const (
	PermProjectRead        Permission = "project:read"
	PermResourceWrite      Permission = "resource:write"
	PermReviewDecide       Permission = "review:decide"
	PermReleasePublish     Permission = "release:publish"
	PermLearningContribute Permission = "learning:contribute"
	PermMembershipManage   Permission = "membership:manage"
	PermTelemetryWrite     Permission = "telemetry:write"
	PermAuditRead          Permission = "audit:read"
	PermOrganizationManage Permission = "organization:manage"
	PermIdentityManage     Permission = "identity:manage"
	PermDeviceManage       Permission = "device:manage"
)

// Scope 是权限作用的层级落点。org 级的 OwnerID 就是 org_id。
type Scope struct {
	Level   resources.Level
	OwnerID string
}

// OrgScope 构造组织级作用域。
func OrgScope(orgID string) Scope { return Scope{Level: resources.LevelOrg, OwnerID: orgID} }

// TeamScope 构造团队级作用域。
func TeamScope(teamID string) Scope { return Scope{Level: resources.LevelTeam, OwnerID: teamID} }

// ProjectScope 构造项目级作用域。
func ProjectScope(projectID string) Scope {
	return Scope{Level: resources.LevelProject, OwnerID: projectID}
}

// Subject 是某成员的全部角色关系，一次加载、多次判断。
type Subject struct {
	UserID  string
	OrgID   string
	OrgRole identity.Role
	// TeamRoles: team_id -> 角色
	TeamRoles map[string]MemberRole
	// ProjectRoles: project_id -> 角色
	ProjectRoles map[string]MemberRole
	// ProjectTeam: project_id -> 所属 team_id（无团队则不出现）
	ProjectTeam map[string]string
	// Reviewer: "level:owner_id" -> 是否被额外指定为 reviewer
	Reviewer map[string]bool
}

func (s Subject) isOrgAdmin() bool { return s.OrgRole.IsAdmin() }

// isMember 判断是否属于某层级：org 级看组织成员身份，team 级看团队成员，
// project 级看项目成员或所属团队成员。
func (s Subject) isMember(sc Scope) bool {
	switch sc.Level {
	case resources.LevelOrg:
		return sc.OwnerID == s.OrgID
	case resources.LevelTeam:
		_, ok := s.TeamRoles[sc.OwnerID]
		return ok
	case resources.LevelProject:
		if _, ok := s.ProjectRoles[sc.OwnerID]; ok {
			return true
		}
		if teamID, ok := s.ProjectTeam[sc.OwnerID]; ok {
			_, isTeam := s.TeamRoles[teamID]
			return isTeam
		}
	}
	return false
}

// isAdminOf 判断是否是某层级的管理员。组织 admin 对一切层级都是；
// 团队 admin 对该团队及其项目都是。
func (s Subject) isAdminOf(sc Scope) bool {
	if s.isOrgAdmin() {
		return true
	}
	switch sc.Level {
	case resources.LevelTeam:
		return s.TeamRoles[sc.OwnerID] == MemberRoleAdmin
	case resources.LevelProject:
		if s.ProjectRoles[sc.OwnerID] == MemberRoleAdmin {
			return true
		}
		if teamID, ok := s.ProjectTeam[sc.OwnerID]; ok {
			return s.TeamRoles[teamID] == MemberRoleAdmin
		}
	}
	return false
}

// Can 判断主体在某作用域是否拥有权限。这是唯一的权限判定入口。
//
// viewer 是后台的只读演示角色：除读之外一律拒绝，包括提交变更集与分享经验。
func (s Subject) Can(p Permission, sc Scope) bool {
	if sc.Level == resources.LevelOrg && sc.OwnerID != s.OrgID {
		return false
	}
	if s.OrgRole == identity.RoleViewer {
		return p == PermProjectRead
	}
	switch p {
	case PermProjectRead:
		return s.isOrgAdmin() || s.isMember(sc)
	case PermResourceWrite, PermLearningContribute, PermTelemetryWrite:
		return s.isOrgAdmin() || s.isMember(sc)
	case PermReviewDecide:
		return s.isAdminOf(sc) || s.Reviewer[scopeKey(sc)]
	case PermReleasePublish, PermMembershipManage:
		return s.isAdminOf(sc)
	case PermAuditRead, PermIdentityManage, PermDeviceManage:
		return s.isOrgAdmin()
	case PermOrganizationManage:
		return s.OrgRole == identity.RoleOwner
	}
	return false
}

// Permissions 列出主体在某作用域拥有的全部权限，供 /v1/me 与前端按钮显隐使用。
func (s Subject) Permissions(sc Scope) []Permission {
	all := []Permission{
		PermProjectRead, PermResourceWrite, PermReviewDecide, PermReleasePublish,
		PermLearningContribute, PermMembershipManage, PermTelemetryWrite, PermAuditRead,
		PermOrganizationManage, PermIdentityManage, PermDeviceManage,
	}
	out := make([]Permission, 0, len(all))
	for _, p := range all {
		if s.Can(p, sc) {
			out = append(out, p)
		}
	}
	return out
}

func scopeKey(sc Scope) string { return string(sc.Level) + ":" + sc.OwnerID }
