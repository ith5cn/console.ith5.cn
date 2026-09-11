package resources

import (
	"testing"
	"time"
)

var now = time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

const org = "org-1"

func ptr(t time.Time) *time.Time { return &t }

func all() []Resource {
	return []Resource{
		{ID: "r1", OrgID: org, Level: LevelOrg, OwnerID: org, Name: "corp-common", Kind: KindSkill, Version: 3},
		{ID: "r2", OrgID: org, Level: LevelOrg, OwnerID: org, Name: "corp-backend", Kind: KindSkill, Version: 1},
		{ID: "r3", OrgID: org, Level: LevelOrg, OwnerID: org, Name: "corp-archived", Kind: KindSkill, Version: 5, Archived: true},
		{ID: "r4", OrgID: org, Level: LevelOrg, OwnerID: org, Name: "corp-draft", Kind: KindSkill, Version: 0},
		{ID: "r5", OrgID: org, Level: LevelProject, OwnerID: "p1", Name: "proj-only", Kind: KindRule, Version: 2},
		{ID: "r6", OrgID: org, Level: LevelOrg, OwnerID: org, Name: "corp-deleted", Kind: KindSkill, Version: 2, Deleted: true},
		{ID: "r9", OrgID: "org-OTHER", Level: LevelOrg, OwnerID: "org-OTHER", Name: "alien", Kind: KindSkill, Version: 1},
	}
}

func groups() []PermissionGroup {
	return []PermissionGroup{
		{ID: "g1", OrgID: org, Key: "backend-pack", Name: "后端工具包", ResourceIDs: []string{"r1", "r2"}},
		{ID: "g2", OrgID: org, Key: "old-pack", Name: "停用包", Archived: true, ResourceIDs: []string{"r1"}},
		{ID: "g9", OrgID: "org-OTHER", Key: "alien-pack", Name: "别家的包", ResourceIDs: []string{"r1"}},
	}
}

func member(projects ...string) Principal {
	return Principal{UserID: "u1", OrgID: org, ProjectIDs: projects}
}

func names(gs []Grant) []string {
	out := make([]string, len(gs))
	for i, g := range gs {
		out[i] = g.Resource.Name
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func resolve(p Principal, as []Assignment) []Grant {
	return ResolveGrants(p, all(), groups(), as, now)
}

func TestResolve_GroupExpandsAndCarriesSource(t *testing.T) {
	as := []Assignment{{OrgID: org, GroupID: "g1", SubjectType: SubjectUser, SubjectID: "u1"}}
	gs := resolve(member(), as)
	eq(t, names(gs), []string{"corp-backend", "corp-common"})
	via := gs[0].Via
	if len(via) != 1 || via[0].GroupID != "g1" || via[0].GroupKey != "backend-pack" || via[0].GroupName != "后端工具包" {
		t.Fatalf("必须能说清是经由哪个权限组拿到的: %+v", via)
	}
}

func TestResolve_ArchivedOrUnknownGroupIgnored(t *testing.T) {
	for _, gid := range []string{"g2", "nope", "g9"} {
		as := []Assignment{{OrgID: org, GroupID: gid, SubjectType: SubjectOrg}}
		if got := resolve(member(), as); len(got) != 0 {
			t.Fatalf("组 %s 不得授予任何内容，got %v", gid, names(got))
		}
	}
}

func TestResolve_DirectAndMultipleSources(t *testing.T) {
	as := []Assignment{
		{OrgID: org, GroupID: "g1", SubjectType: SubjectOrg},
		{OrgID: org, ResourceID: "r1", SubjectType: SubjectUser, SubjectID: "u1"},
	}
	for _, g := range resolve(member(), as) {
		if g.Resource.ID == "r1" && len(g.Via) != 2 {
			t.Fatalf("两条授权路径应全部记录，got %+v", g.Via)
		}
	}
}

func TestResolve_Exclusions(t *testing.T) {
	p := member()
	p.Suspended = true
	if got := resolve(p, []Assignment{{OrgID: org, GroupID: "g1", SubjectType: SubjectOrg}}); len(got) != 0 {
		t.Fatal("suspended 用户必须拿到空集合")
	}
	as := []Assignment{
		{OrgID: org, ResourceID: "r3", SubjectType: SubjectOrg},
		{OrgID: org, ResourceID: "r4", SubjectType: SubjectOrg},
		{OrgID: org, ResourceID: "r6", SubjectType: SubjectOrg},
		{OrgID: org, ResourceID: "r9", SubjectType: SubjectOrg},
		{OrgID: "org-OTHER", ResourceID: "r1", SubjectType: SubjectOrg},
		{OrgID: org, ResourceID: "r1", SubjectType: SubjectType("role")},
	}
	if got := resolve(member(), as); len(got) != 0 {
		t.Fatalf("archived、无版本、tombstone、跨 org、未知主体都必须排除，got %v", names(got))
	}
}

func TestResolve_ProjectLevelNeedsMembership(t *testing.T) {
	as := []Assignment{{OrgID: org, ResourceID: "r5", SubjectType: SubjectOrg}}
	eq(t, names(resolve(member("p1"), as)), []string{"proj-only"})
	if got := resolve(member("p9"), as); len(got) != 0 {
		t.Fatalf("project 级资源必须校验项目归属，got %v", names(got))
	}
	as = []Assignment{{OrgID: org, ResourceID: "r1", SubjectType: SubjectProject, SubjectID: "p1"}}
	eq(t, names(resolve(member("p1"), as)), []string{"corp-common"})
	if got := resolve(member("p2"), as); len(got) != 0 {
		t.Fatal("非项目成员不得拿到项目主体的授权")
	}
}

func TestResolve_ExpiryBoundary(t *testing.T) {
	cases := []struct {
		name string
		exp  *time.Time
		want int
	}{
		{"已过期", ptr(now.Add(-time.Second)), 0},
		{"恰好到期即失效", ptr(now), 0},
		{"尚未到期", ptr(now.Add(time.Nanosecond)), 1},
		{"nil 为永久", nil, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			as := []Assignment{{OrgID: org, ResourceID: "r1", SubjectType: SubjectUser, SubjectID: "u1", ExpiresAt: c.exp}}
			if got := len(resolve(member(), as)); got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}
