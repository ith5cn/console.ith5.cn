package core

import (
	"testing"
	"time"
)

var now = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

const org = "org-1"

func ptr(t time.Time) *time.Time { return &t }

func bundles() []BundleMeta {
	return []BundleMeta{
		{ID: "b1", OrgID: org, Name: "corp-common", Kind: KindSkill, Scope: ScopeEnterprise, Version: 3, Checksum: "c1"},
		{ID: "b2", OrgID: org, Name: "corp-backend", Kind: KindSkill, Scope: ScopeEnterprise, Version: 1, Checksum: "c2"},
		{ID: "b3", OrgID: org, Name: "corp-archived", Kind: KindSkill, Scope: ScopeEnterprise, Version: 5, Archived: true},
		{ID: "b4", OrgID: org, Name: "corp-draft", Kind: KindSkill, Scope: ScopeEnterprise, Version: 0},
		{ID: "b5", OrgID: org, Name: "corp-proj", Kind: KindCommand, Scope: ScopeProject, ProjectID: "p1", Version: 2},
		{ID: "b9", OrgID: "org-OTHER", Name: "alien", Kind: KindSkill, Scope: ScopeEnterprise, Version: 1},
	}
}

func groups() []PermissionGroup {
	return []PermissionGroup{
		{ID: "g1", OrgID: org, Key: "backend-pack", Name: "后端工具包", BundleIDs: []string{"b1", "b2"}},
		{ID: "g2", OrgID: org, Key: "old-pack", Name: "停用包", Archived: true, BundleIDs: []string{"b1"}},
		{ID: "g9", OrgID: "org-OTHER", Key: "alien-pack", Name: "别家的包", BundleIDs: []string{"b1"}},
	}
}

func member(projects ...string) Principal {
	return Principal{UserID: "u1", OrgID: org, Role: "member", ProjectIDs: projects}
}

func names(gs []Grant) []string {
	out := make([]string, len(gs))
	for i, g := range gs {
		out[i] = g.Bundle.Name
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
	return Resolve(p, bundles(), groups(), as, now)
}

// ---- 权限组展开 ----

func TestResolve_GroupExpandsToAllBundles(t *testing.T) {
	as := []Assignment{{OrgID: org, GroupID: "g1", SubjectType: SubjectUser, SubjectID: "u1"}}
	eq(t, names(resolve(member(), as)), []string{"corp-backend", "corp-common"})
}

func TestResolve_GroupCarriesGrantSource(t *testing.T) {
	as := []Assignment{{OrgID: org, GroupID: "g1", SubjectType: SubjectUser, SubjectID: "u1"}}
	gs := resolve(member(), as)
	if len(gs) == 0 {
		t.Fatal("应有结果")
	}
	via := gs[0].Via
	if len(via) != 1 || via[0].GroupID != "g1" || via[0].GroupName != "后端工具包" {
		t.Fatalf("必须能说清是经由哪个权限组拿到的: %+v", via)
	}
}

func TestResolve_ArchivedGroupExcluded(t *testing.T) {
	as := []Assignment{{OrgID: org, GroupID: "g2", SubjectType: SubjectOrg}}
	if got := resolve(member(), as); len(got) != 0 {
		t.Fatalf("已归档权限组必须排除，got %v", names(got))
	}
}

func TestResolve_UnknownGroupIgnored(t *testing.T) {
	as := []Assignment{{OrgID: org, GroupID: "nope", SubjectType: SubjectOrg}}
	if got := resolve(member(), as); len(got) != 0 {
		t.Fatal("不存在的权限组不得授予任何内容")
	}
}

// 直接授权单个 bundle（一次性/临时授权路径）
func TestResolve_DirectBundleAssignment(t *testing.T) {
	as := []Assignment{{OrgID: org, BundleID: "b2", SubjectType: SubjectUser, SubjectID: "u1"}}
	gs := resolve(member(), as)
	eq(t, names(gs), []string{"corp-backend"})
	if gs[0].Via[0].GroupID != "" {
		t.Fatal("直接授权不应带权限组来源")
	}
}

// 同一 bundle 经多条路径拿到时，来源要全部记录（排障需要）
func TestResolve_MultipleSourcesRecorded(t *testing.T) {
	as := []Assignment{
		{OrgID: org, GroupID: "g1", SubjectType: SubjectOrg},
		{OrgID: org, BundleID: "b1", SubjectType: SubjectUser, SubjectID: "u1"},
	}
	for _, g := range resolve(member(), as) {
		if g.Bundle.ID == "b1" && len(g.Via) != 2 {
			t.Fatalf("corp-common 有两条授权路径，应全部记录，got %+v", g.Via)
		}
	}
}

// ---- 基础规则 ----

func TestResolve_SuspendedGetsNothing(t *testing.T) {
	p := member()
	p.Suspended = true
	as := []Assignment{{OrgID: org, GroupID: "g1", SubjectType: SubjectOrg}}
	if got := resolve(p, as); len(got) != 0 {
		t.Fatalf("suspended 用户必须拿到空集合，got %v", names(got))
	}
}

func TestResolve_ArchivedAndUnpublishedExcluded(t *testing.T) {
	as := []Assignment{
		{OrgID: org, BundleID: "b3", SubjectType: SubjectOrg},
		{OrgID: org, BundleID: "b4", SubjectType: SubjectOrg},
	}
	if got := resolve(member(), as); len(got) != 0 {
		t.Fatalf("archived 与无已发布版本必须排除，got %v", names(got))
	}
}

func TestResolve_ProjectSubject(t *testing.T) {
	as := []Assignment{{OrgID: org, BundleID: "b1", SubjectType: SubjectProject, SubjectID: "p1"}}
	eq(t, names(resolve(member("p1"), as)), []string{"corp-common"})
	if got := resolve(member("p2"), as); len(got) != 0 {
		t.Fatalf("非项目成员不得拿到，got %v", names(got))
	}
}

func TestResolve_UnknownSubjectTypeDenied(t *testing.T) {
	as := []Assignment{{OrgID: org, BundleID: "b1", SubjectType: SubjectType("role")}}
	if got := resolve(member(), as); len(got) != 0 {
		t.Fatal("未知 subject 类型必须拒绝（'role' 已从模型中移除）")
	}
}

// ---- expires_at 边界（PRD C5，闭区间到期）----

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
			as := []Assignment{{OrgID: org, BundleID: "b1", SubjectType: SubjectUser, SubjectID: "u1", ExpiresAt: c.exp}}
			if got := len(resolve(member(), as)); got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}

// ---- scope=project ----

func TestResolve_ProjectScopeBundle_NeedsMembership(t *testing.T) {
	// 即使被 org 级授权命中，scope=project 的 Bundle 仍需项目归属一致
	as := []Assignment{{OrgID: org, BundleID: "b5", SubjectType: SubjectOrg}}
	eq(t, names(resolve(member("p1"), as)), []string{"corp-proj"})
	if got := resolve(member("p9"), as); len(got) != 0 {
		t.Fatalf("scope=project 必须校验项目归属，got %v", names(got))
	}
}

// ---- 跨 org 纵深防御 ----

func TestResolve_CrossOrgBundleRejected(t *testing.T) {
	as := []Assignment{{OrgID: org, BundleID: "b9", SubjectType: SubjectOrg}}
	if got := resolve(member(), as); len(got) != 0 {
		t.Fatal("跨 org 的 Bundle 必须拒绝，即便 db 层漏了 WHERE")
	}
}

func TestResolve_CrossOrgAssignmentRejected(t *testing.T) {
	as := []Assignment{{OrgID: "org-OTHER", BundleID: "b1", SubjectType: SubjectOrg}}
	if got := resolve(member(), as); len(got) != 0 {
		t.Fatal("跨 org 的 Assignment 必须拒绝")
	}
}

func TestResolve_CrossOrgGroupRejected(t *testing.T) {
	as := []Assignment{{OrgID: org, GroupID: "g9", SubjectType: SubjectOrg}}
	if got := resolve(member(), as); len(got) != 0 {
		t.Fatal("跨 org 的权限组必须拒绝")
	}
}

// ---- 排序 ----

func TestResolve_StableOrder_KindThenName(t *testing.T) {
	as := []Assignment{
		{OrgID: org, GroupID: "g1", SubjectType: SubjectOrg},
		{OrgID: org, BundleID: "b5", SubjectType: SubjectOrg},
	}
	// command < skill（字典序），故 corp-proj 在前
	eq(t, names(resolve(member("p1"), as)), []string{"corp-proj", "corp-backend", "corp-common"})
}

func TestBundles(t *testing.T) {
	as := []Assignment{{OrgID: org, GroupID: "g1", SubjectType: SubjectOrg}}
	bs := Bundles(resolve(member(), as))
	if len(bs) != 2 || bs[0].Name != "corp-backend" {
		t.Fatalf("got %+v", bs)
	}
}
