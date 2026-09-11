package identity

import "testing"

func TestReconcile(t *testing.T) {
	mappings := []GroupMapping{
		{IdPGroup: "eng", Target: "org", Role: "member", Priority: 1},
		{IdPGroup: "leads", Target: "org", Role: "admin", Priority: 2},
		{IdPGroup: "billing-dev", Target: "project", TargetID: "p1", Role: "member", Priority: 1},
	}
	members := []OIDCMember{
		// 映射未变，无差异
		{UserID: "u1", Email: "a@x", Role: RoleMember, Groups: []string{"eng", "billing-dev"}, ProjectRoles: map[string]string{"p1": "member"}},
		// 被加进 leads 后未再登录：应升 admin
		{UserID: "u2", Email: "b@x", Role: RoleMember, Groups: []string{"eng", "leads"}},
		// 项目映射被删掉：应移出 p2；无 org 映射命中：组织角色不动
		{UserID: "u3", Email: "c@x", Role: RoleOwner, Groups: []string{"ops"}, ProjectRoles: map[string]string{"p2": "admin"}},
	}
	diffs := Reconcile(mappings, members)
	if len(diffs) != 2 {
		t.Fatalf("差异数 = %d, 想要 2: %+v", len(diffs), diffs)
	}
	if diffs[0].UserID != "u2" || diffs[0].Changes[0].Target != "org" || diffs[0].Changes[0].Expected != "admin" {
		t.Fatalf("u2 差异不对: %+v", diffs[0])
	}
	if diffs[1].UserID != "u3" || diffs[1].OrgRole != "" || diffs[1].Changes[0].Target != "project" || diffs[1].Changes[0].Expected != "" {
		t.Fatalf("u3 差异不对: %+v", diffs[1])
	}
}
