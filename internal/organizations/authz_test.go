package organizations

import (
	"testing"

	"github.com/ith5/ith5/internal/identity"
)

func subject(orgRole identity.Role) Subject {
	return Subject{
		UserID: "u1", OrgID: "org", OrgRole: orgRole,
		TeamRoles:    map[string]MemberRole{},
		ProjectRoles: map[string]MemberRole{},
		ProjectTeam:  map[string]string{"p-in-team": "t1"},
		Reviewer:     map[string]bool{},
	}
}

func TestCan_OrgRoles(t *testing.T) {
	owner, admin, member, viewer := subject(identity.RoleOwner), subject(identity.RoleAdmin), subject(identity.RoleMember), subject(identity.RoleViewer)
	org := OrgScope("org")

	if !owner.Can(PermOrganizationManage, org) || admin.Can(PermOrganizationManage, org) {
		t.Fatal("organization:manage 只有 owner")
	}
	for _, p := range []Permission{PermAuditRead, PermIdentityManage, PermDeviceManage, PermMembershipManage, PermReleasePublish, PermReviewDecide} {
		if !admin.Can(p, org) || member.Can(p, org) {
			t.Fatalf("%s 应 admin 有、member 无", p)
		}
	}
	for _, p := range []Permission{PermProjectRead, PermResourceWrite, PermLearningContribute, PermTelemetryWrite} {
		if !member.Can(p, org) {
			t.Fatalf("组织成员应有 %s", p)
		}
	}
	if !viewer.Can(PermProjectRead, org) || viewer.Can(PermResourceWrite, org) || viewer.Can(PermLearningContribute, org) {
		t.Fatal("viewer 只能读")
	}
	if member.Can(PermProjectRead, OrgScope("other-org")) {
		t.Fatal("跨组织作用域一律拒绝")
	}
}

func TestCan_TeamAndProject(t *testing.T) {
	m := subject(identity.RoleMember)
	m.TeamRoles["t1"] = MemberRoleMember
	m.ProjectRoles["p2"] = MemberRoleAdmin

	// 团队成员可读团队项目、可提变更集，但不能审、不能发
	if !m.Can(PermProjectRead, ProjectScope("p-in-team")) || !m.Can(PermResourceWrite, ProjectScope("p-in-team")) {
		t.Fatal("团队成员应能读写团队下的项目")
	}
	if m.Can(PermReviewDecide, ProjectScope("p-in-team")) || m.Can(PermReleasePublish, ProjectScope("p-in-team")) {
		t.Fatal("团队普通成员不能审核或发布")
	}
	// 项目 admin 可审可发可管成员
	for _, p := range []Permission{PermReviewDecide, PermReleasePublish, PermMembershipManage} {
		if !m.Can(p, ProjectScope("p2")) {
			t.Fatalf("项目 admin 应有 %s", p)
		}
	}
	// 与自己无关的项目
	if m.Can(PermProjectRead, ProjectScope("p-other")) {
		t.Fatal("非成员不能读")
	}
	// 团队 admin 对团队下项目有管理权
	m.TeamRoles["t1"] = MemberRoleAdmin
	if !m.Can(PermReleasePublish, ProjectScope("p-in-team")) || !m.Can(PermMembershipManage, TeamScope("t1")) {
		t.Fatal("团队 admin 应能管理团队及其项目")
	}
}

func TestCan_ReviewerListAndAuthorRuleLeftToService(t *testing.T) {
	m := subject(identity.RoleMember)
	m.ProjectRoles["p1"] = MemberRoleMember
	if m.Can(PermReviewDecide, ProjectScope("p1")) {
		t.Fatal("普通项目成员默认不是 reviewer")
	}
	m.Reviewer["project:p1"] = true
	if !m.Can(PermReviewDecide, ProjectScope("p1")) {
		t.Fatal("额外指定的 reviewer 应能审核")
	}
	if m.Can(PermReleasePublish, ProjectScope("p1")) {
		t.Fatal("reviewer 不等于发布者")
	}
}

func TestPermissions_ListIsConsistentWithCan(t *testing.T) {
	a := subject(identity.RoleAdmin)
	got := a.Permissions(OrgScope("org"))
	seen := map[Permission]bool{}
	for _, p := range got {
		seen[p] = true
		if !a.Can(p, OrgScope("org")) {
			t.Fatalf("列表里的 %s 应可通过 Can", p)
		}
	}
	if seen[PermOrganizationManage] {
		t.Fatal("admin 不应列出 organization:manage")
	}
}
