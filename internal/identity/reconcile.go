package identity

import (
	"context"
	"sort"
	"time"
)

// OIDCMember 是一个经 OIDC 建档的成员当前在库里的样子，用于与映射表对账。
type OIDCMember struct {
	UserID       string
	Email        string
	Role         Role
	Groups       []string
	TeamRoles    map[string]string // team_id -> role
	ProjectRoles map[string]string // project_id -> role
	SyncedAt     time.Time
}

// RoleChange 是一处角色差异；Current 为空表示应新增，Expected 为空表示应移除。
type RoleChange struct {
	Target   string `json:"target"` // org | team | project
	TargetID string `json:"target_id,omitempty"`
	Current  string `json:"current,omitempty"`
	Expected string `json:"expected,omitempty"`
}

// ReconcileDiff 是一个成员的全部差异。
type ReconcileDiff struct {
	UserID   string       `json:"user_id"`
	Email    string       `json:"email"`
	SyncedAt time.Time    `json:"synced_at"`
	Changes  []RoleChange `json:"changes"`

	// 折算结果，apply 时直接落库
	OrgRole      Role              `json:"-"`
	TeamRoles    map[string]string `json:"-"`
	ProjectRoles map[string]string `json:"-"`
}

// ReconcileStore 是对账需要的持久化能力。
type ReconcileStore interface {
	ListOIDCMembers(ctx context.Context, orgID string) ([]OIDCMember, error)
	// ApplyMembership 按折算结果重写一个成员的组织 / 团队 / 项目角色。orgRole 为空时保持组织角色不变。
	ApplyMembership(ctx context.Context, orgID, userID string, orgRole Role, teamRoles, projectRoles map[string]string) error
}

// Reconcile 用当前映射表重算每个 OIDC 成员的角色，返回与库中不一致的成员。
//
// 场景：管理员改了分组映射，已登录过的成员要等下次登录才会重算；对账把这段空窗补上。
// 组织角色折算为空（没有任何 org 映射命中）时不动组织角色，只对账团队与项目，
// 避免把一个手工提升的 owner 降级。
func Reconcile(mappings []GroupMapping, members []OIDCMember) []ReconcileDiff {
	var diffs []ReconcileDiff
	for _, m := range members {
		orgRole, teamRoles, projectRoles := ResolveGroupMappings(mappings, m.Groups)
		d := ReconcileDiff{UserID: m.UserID, Email: m.Email, SyncedAt: m.SyncedAt, OrgRole: orgRole, TeamRoles: teamRoles, ProjectRoles: projectRoles}
		if orgRole != "" && orgRole != m.Role {
			d.Changes = append(d.Changes, RoleChange{Target: "org", Current: string(m.Role), Expected: string(orgRole)})
		}
		d.Changes = append(d.Changes, diffRoles("team", m.TeamRoles, teamRoles)...)
		d.Changes = append(d.Changes, diffRoles("project", m.ProjectRoles, projectRoles)...)
		if len(d.Changes) > 0 {
			diffs = append(diffs, d)
		}
	}
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Email < diffs[j].Email })
	return diffs
}

func diffRoles(target string, current, expected map[string]string) []RoleChange {
	ids := map[string]bool{}
	for id := range current {
		ids[id] = true
	}
	for id := range expected {
		ids[id] = true
	}
	var out []RoleChange
	for id := range ids {
		if current[id] != expected[id] {
			out = append(out, RoleChange{Target: target, TargetID: id, Current: current[id], Expected: expected[id]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TargetID < out[j].TargetID })
	return out
}
