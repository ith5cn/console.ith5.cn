package identity

import "testing"

func TestResolveGroupMappings(t *testing.T) {
	mappings := []GroupMapping{
		{IdPGroup: "eng", Target: "org", Role: "member", Priority: 0},
		{IdPGroup: "eng-leads", Target: "org", Role: "admin", Priority: 10},
		{IdPGroup: "eng", Target: "team", TargetID: "t1", Role: "member"},
		{IdPGroup: "eng-leads", Target: "team", TargetID: "t1", Role: "admin"},
		{IdPGroup: "billing", Target: "project", TargetID: "p1", Role: "member"},
	}

	orgRole, teams, projects := ResolveGroupMappings(mappings, []string{"eng"})
	if orgRole != RoleMember || teams["t1"] != "member" || len(projects) != 0 {
		t.Fatalf("普通工程师: %s %v %v", orgRole, teams, projects)
	}

	orgRole, teams, projects = ResolveGroupMappings(mappings, []string{"eng", "eng-leads", "billing"})
	if orgRole != RoleAdmin {
		t.Fatalf("高优先级映射应胜出: %s", orgRole)
	}
	if teams["t1"] != "admin" || projects["p1"] != "member" {
		t.Fatalf("团队与项目角色: %v %v", teams, projects)
	}

	orgRole, _, _ = ResolveGroupMappings(mappings, []string{"unrelated"})
	if orgRole != "" {
		t.Fatalf("没有命中时组织角色应为空: %s", orgRole)
	}
}

func TestResolveGroupMappings_SamePriorityHigherRoleWins(t *testing.T) {
	mappings := []GroupMapping{
		{IdPGroup: "a", Target: "org", Role: "viewer"},
		{IdPGroup: "b", Target: "org", Role: "member"},
	}
	orgRole, _, _ := ResolveGroupMappings(mappings, []string{"a", "b"})
	if orgRole != RoleMember {
		t.Fatalf("同优先级取更高角色: %s", orgRole)
	}
}
