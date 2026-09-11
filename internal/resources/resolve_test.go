package resources

import "testing"

func cand(id string, level Level, owner string, kind Kind, name string, ver int, sum string) Candidate {
	return Candidate{
		Resource: Resource{ID: id, OrgID: "org", Level: level, OwnerID: owner, Kind: kind, Name: name, Version: ver, Checksum: sum},
		Files:    []FileRef{{Path: kind.EntryFile(), SHA256: sum, Size: 10}},
	}
}

func find(out ResolveOutput, kind Kind, name string) (Entry, bool) {
	for _, e := range out.Entries {
		if e.Kind == kind && e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

func TestResolve_ScopeAndSpecificity(t *testing.T) {
	in := ResolveInput{
		OrgID: "org", TeamIDs: []string{"t1"}, ProjectIDs: []string{"p1"},
		Candidates: []Candidate{
			cand("r1", LevelOrg, "org", KindSkill, "deploy", 3, "a"),
			cand("r2", LevelTeam, "t1", KindSkill, "deploy", 1, "b"),    // 团队覆盖组织
			cand("r3", LevelProject, "p1", KindSkill, "deploy", 5, "c"), // 项目再覆盖
			cand("r4", LevelProject, "p9", KindSkill, "other", 1, "d"),  // 未绑定的项目
			cand("r5", LevelTeam, "t9", KindRule, "naming", 1, "e"),     // 不属于的团队
			cand("r6", LevelOrg, "org", KindRule, "naming", 2, "f"),
			cand("r7", LevelOrg, "other-org", KindSkill, "alien", 1, "g"), // 跨 org
		},
	}
	out := Resolve(in)
	e, ok := find(out, KindSkill, "deploy")
	if !ok || e.Version != 5 || e.Level != LevelProject || e.Size != 10 {
		t.Fatalf("应取最具体层级: %+v", e)
	}
	if _, ok := find(out, KindSkill, "other"); ok {
		t.Fatal("未绑定项目的资源不应出现")
	}
	if e, ok := find(out, KindRule, "naming"); !ok || e.Version != 2 {
		t.Fatalf("不属于的团队不应覆盖组织级: %+v", e)
	}
	if _, ok := find(out, KindSkill, "alien"); ok {
		t.Fatal("跨 org 资源必须排除")
	}
}

func TestResolve_TombstoneMasksUpper(t *testing.T) {
	dead := cand("r2", LevelProject, "p1", KindSkill, "deploy", 2, "x")
	dead.Deleted = true
	out := Resolve(ResolveInput{OrgID: "org", ProjectIDs: []string{"p1"}, Candidates: []Candidate{
		cand("r1", LevelOrg, "org", KindSkill, "deploy", 3, "a"), dead,
	}})
	if _, ok := find(out, KindSkill, "deploy"); ok {
		t.Fatal("项目级 tombstone 应屏蔽组织级同名资源")
	}
}

func TestResolve_ProjectConflict(t *testing.T) {
	out := Resolve(ResolveInput{OrgID: "org", ProjectIDs: []string{"p1", "p2"}, Candidates: []Candidate{
		cand("r1", LevelProject, "p1", KindRule, "naming", 3, "a"),
		cand("r2", LevelProject, "p2", KindRule, "naming", 5, "b"),
		cand("r3", LevelProject, "p1", KindRule, "same", 1, "s"),
		cand("r4", LevelProject, "p2", KindRule, "same", 2, "s"), // 内容相同不算冲突
	}})
	e, ok := find(out, KindRule, "naming")
	if !ok || e.Status != StatusConflict || e.Conflict == nil || len(e.Conflict.Owners) != 2 || len(e.Files) != 0 {
		t.Fatalf("同层级不同内容应标 conflict 且不带 files: %+v", e)
	}
	if e, ok := find(out, KindRule, "same"); !ok || e.Status != StatusOK {
		t.Fatalf("内容相同不应冲突: %+v", e)
	}
}

func TestResolve_GroupIsLowestPriority(t *testing.T) {
	viaGroup := cand("r1", LevelOrg, "org", KindSkill, "deploy", 9, "g")
	viaGroup.ViaGroup = "backend-pack"
	viaGroup.Namespace = "backend-pack"
	out := Resolve(ResolveInput{OrgID: "org", ProjectIDs: []string{"p1"}, Candidates: []Candidate{
		viaGroup, cand("r2", LevelProject, "p1", KindSkill, "deploy", 1, "p"),
	}})
	if e, _ := find(out, KindSkill, "deploy"); e.Version != 1 {
		t.Fatalf("层级资源应胜过权限组资源: %+v", e)
	}
	out = Resolve(ResolveInput{OrgID: "org", Candidates: []Candidate{viaGroup}})
	if e, ok := find(out, KindSkill, "deploy"); !ok || e.Namespace != "backend-pack" {
		t.Fatalf("单独的权限组资源应出现并带命名空间: %+v", e)
	}
}

func TestResolve_PolicyMergeAndCulture(t *testing.T) {
	orgPol := cand("p1", LevelOrg, "org", KindPolicy, "policy", 1, "a")
	orgPol.Content = "enforced_rules: [security]\nmcp_allowed_hosts: [\"*.corp\", \"x.corp\"]\nhooks_auto_apply: false\nrecall_enabled: false\n"
	projPol := cand("p2", LevelProject, "p1", KindPolicy, "policy", 1, "b")
	projPol.Content = "enforced_rules: [naming]\nmcp_allowed_hosts: [\"x.corp\", \"evil.com\"]\nhooks_auto_apply: true\nrecall_enabled: true\n"
	orgCul := cand("c1", LevelOrg, "org", KindCulture, "culture", 1, "c")
	projCul := cand("c2", LevelProject, "p1", KindCulture, "culture", 2, "d")

	out := Resolve(ResolveInput{OrgID: "org", ProjectIDs: []string{"p1"}, Candidates: []Candidate{orgPol, projPol, orgCul, projCul}})
	p := out.Policy
	if len(p.EnforcedRules) != 2 || p.EnforcedRules[0] != "naming" || p.EnforcedRules[1] != "security" {
		t.Fatalf("enforced 应取并集: %v", p.EnforcedRules)
	}
	if p.HooksAutoApply {
		t.Fatal("上层 false 下层不能改 true")
	}
	if len(p.MCPAllowedHosts) != 1 || p.MCPAllowedHosts[0] != "x.corp" {
		t.Fatalf("allowed hosts 只能收窄: %v", p.MCPAllowedHosts)
	}
	if !p.RecallEnabled {
		t.Fatal("recall 下层可覆盖")
	}
	if out.Culture == nil || out.Culture.Version != 2 {
		t.Fatalf("culture 应取最具体: %+v", out.Culture)
	}
	if _, ok := find(out, KindPolicy, "policy"); ok {
		t.Fatal("policy 不应作为普通条目出现")
	}
}

func TestParsePolicy_Defaults(t *testing.T) {
	p, ok := ParsePolicy("co_author: no\n")
	if !ok || p.CoAuthor || !p.HooksAutoApply || !p.MCPAutoApply || !p.ContributeHint {
		t.Fatalf("未写的键应取默认: %+v", p)
	}
	if _, ok := ParsePolicy("   "); ok {
		t.Fatal("空内容应返回 false")
	}
}
