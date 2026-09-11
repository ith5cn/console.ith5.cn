package changesets

import (
	"context"
	"errors"
	"testing"

	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/resources"
)

// fakeStore 是最小内存实现，只覆盖状态机需要的行为。
type fakeStore struct {
	cs      map[string]Changeset
	blobs   map[string][]byte
	heads   map[string]string
	current map[string]int
	next    int
}

func newFake() *fakeStore {
	return &fakeStore{cs: map[string]Changeset{}, blobs: map[string][]byte{}, heads: map[string]string{}, current: map[string]int{}}
}

func (f *fakeStore) Create(_ context.Context, cs Changeset) (string, error) {
	f.next++
	cs.ID = "cs-" + string(rune('0'+f.next))
	cs.ETag = "e1"
	f.cs[cs.ID] = cs
	return cs.ID, nil
}

func (f *fakeStore) Get(_ context.Context, orgID, id string) (Changeset, error) {
	cs, ok := f.cs[id]
	if !ok || cs.OrgID != orgID {
		return Changeset{}, ErrNotFound
	}
	return cs, nil
}

func (f *fakeStore) List(context.Context, string, ListFilter) ([]Changeset, error) { return nil, nil }

func (f *fakeStore) Update(_ context.Context, cs Changeset, ifMatch string) error {
	cur := f.cs[cs.ID]
	if cur.ETag != ifMatch {
		return ErrETagMismatch
	}
	cs.ETag = cur.ETag + "x"
	cs.Reviews = cur.Reviews
	f.cs[cs.ID] = cs
	return nil
}

func (f *fakeStore) SetState(_ context.Context, _ string, id string, state State, digest *string) error {
	cs := f.cs[id]
	cs.State = state
	if digest != nil {
		cs.SubmittedDigest = *digest
	}
	f.cs[id] = cs
	return nil
}

func (f *fakeStore) AddReview(_ context.Context, _ string, id string, r Review) error {
	cs := f.cs[id]
	cs.Reviews = append(cs.Reviews, r)
	f.cs[id] = cs
	return nil
}

func (f *fakeStore) SupersedeReviews(_ context.Context, _ string, id string) error {
	cs := f.cs[id]
	for i := range cs.Reviews {
		cs.Reviews[i].Superseded = true
	}
	f.cs[id] = cs
	return nil
}

func (f *fakeStore) MissingBlobs(_ context.Context, _ string, shas []string) ([]string, error) {
	var out []string
	for _, s := range shas {
		if _, ok := f.blobs[s]; !ok {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeStore) GetBlob(_ context.Context, _ string, sha string) ([]byte, error) {
	b, ok := f.blobs[sha]
	if !ok {
		return nil, ErrMissingBlob
	}
	return b, nil
}

func (f *fakeStore) HeadRevisions(_ context.Context, _ string, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := f.heads[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (f *fakeStore) ScopeExists(_ context.Context, orgID string, sc organizations.Scope) (bool, error) {
	return sc.Level == resources.LevelOrg && sc.OwnerID == orgID || sc.OwnerID == "p1", nil
}

func (f *fakeStore) CurrentVersion(_ context.Context, _ string, sc organizations.Scope, kind resources.Kind, name string) (int, bool, error) {
	return f.current[ScopeKey(sc)+"/"+string(kind)+"/"+name], false, nil
}

type fakePolicy struct{ n int }

func (p fakePolicy) GetPolicy(context.Context, string) (organizations.Policy, error) {
	return organizations.Policy{RequiredApprovals: p.n}, nil
}

func subject(userID string, role identity.Role, projectAdmin bool) organizations.Subject {
	s := organizations.Subject{
		UserID: userID, OrgID: "org", OrgRole: role,
		TeamRoles: map[string]organizations.MemberRole{}, ProjectRoles: map[string]organizations.MemberRole{},
		ProjectTeam: map[string]string{}, Reviewer: map[string]bool{},
	}
	if projectAdmin {
		s.ProjectRoles["p1"] = organizations.MemberRoleAdmin
	} else {
		s.ProjectRoles["p1"] = organizations.MemberRoleMember
	}
	return s
}

func putOp(f *fakeStore, name, content string) Op {
	sum := resources.BlobSum([]byte(content))
	f.blobs[sum] = []byte(content)
	return Op{Op: OpPut, Level: resources.LevelProject, ProjectID: "p1", Kind: resources.KindRule, Name: name,
		Files: []resources.FileRef{{Path: "RULE.md", SHA256: sum, Size: len(content)}}}
}

func TestLifecycle_ReviewThenApprove(t *testing.T) {
	f := newFake()
	svc := NewService(f, fakePolicy{1}, nil)
	ctx := context.Background()
	author := Actor{UserID: "u-author", OrgID: "org", Subject: subject("u-author", identity.RoleMember, false)}
	reviewer := Actor{UserID: "u-admin", OrgID: "org", Subject: subject("u-admin", identity.RoleAdmin, true)}

	cs, err := svc.Create(ctx, author, Input{Title: "naming", Ops: []Op{putOp(f, "naming", "# naming")}})
	if err != nil {
		t.Fatal(err)
	}
	if cs.State != StateDraft || cs.BaseRevisions["project:p1"] != "" {
		t.Fatalf("新建应为 draft 且基线记空: %+v", cs)
	}
	// 未提交不能审
	if _, err := svc.Review(ctx, reviewer, cs.ID, DecisionApprove, "", ""); !errors.Is(err, ErrState) {
		t.Fatalf("draft 不能审: %v", err)
	}
	cs, err = svc.Submit(ctx, author, cs.ID)
	if err != nil || cs.State != StateInReview || cs.SubmittedDigest == "" {
		t.Fatalf("submit: %v %+v", err, cs)
	}
	// 作者不能审自己；digest 不对不算
	if _, err := svc.Review(ctx, author, cs.ID, DecisionApprove, cs.SubmittedDigest, ""); !errors.Is(err, ErrSelfReview) {
		t.Fatalf("自审: %v", err)
	}
	if _, err := svc.Review(ctx, reviewer, cs.ID, DecisionApprove, "sha256:stale", ""); !errors.Is(err, ErrDigest) {
		t.Fatalf("digest 不符: %v", err)
	}
	// 普通成员不是 reviewer
	member := Actor{UserID: "u-m", OrgID: "org", Subject: subject("u-m", identity.RoleMember, false)}
	if _, err := svc.Review(ctx, member, cs.ID, DecisionApprove, cs.SubmittedDigest, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("非 reviewer: %v", err)
	}
	cs, err = svc.Review(ctx, reviewer, cs.ID, DecisionApprove, cs.SubmittedDigest, "lgtm")
	if err != nil || cs.State != StateApproved {
		t.Fatalf("approve: %v %+v", err, cs)
	}
	// APPROVED 后作者再编辑 → 回到 draft，审核作废
	cs, err = svc.Edit(ctx, author, cs.ID, cs.ETag, Input{Title: "naming v2", Ops: []Op{putOp(f, "naming", "# naming v2")}})
	if err != nil || cs.State != StateDraft || cs.SubmittedDigest != "" || !cs.Reviews[0].Superseded {
		t.Fatalf("编辑应打回草稿并作废审核: %v %+v", err, cs)
	}
	if _, err := svc.Edit(ctx, author, cs.ID, "stale-etag", Input{Title: "x", Ops: cs.Ops}); !errors.Is(err, ErrETagMismatch) {
		t.Fatalf("过期 etag: %v", err)
	}
}

func TestLifecycle_RequiredApprovalsAndRequestChanges(t *testing.T) {
	f := newFake()
	svc := NewService(f, fakePolicy{2}, nil)
	ctx := context.Background()
	author := Actor{UserID: "u-author", OrgID: "org", Subject: subject("u-author", identity.RoleMember, false)}
	r1 := Actor{UserID: "u-r1", OrgID: "org", Subject: subject("u-r1", identity.RoleAdmin, true)}
	r2 := Actor{UserID: "u-r2", OrgID: "org", Subject: subject("u-r2", identity.RoleAdmin, true)}

	cs, _ := svc.Create(ctx, author, Input{Title: "t", Ops: []Op{putOp(f, "a", "# a")}})
	cs, _ = svc.Submit(ctx, author, cs.ID)
	cs, err := svc.Review(ctx, r1, cs.ID, DecisionApprove, cs.SubmittedDigest, "")
	if err != nil || cs.State != StateInReview {
		t.Fatalf("一人批准不够: %v %s", err, cs.State)
	}
	// 同一人重复批准不计数
	cs, _ = svc.Review(ctx, r1, cs.ID, DecisionApprove, cs.SubmittedDigest, "")
	if cs.State != StateInReview {
		t.Fatal("同一人重复批准不应计数")
	}
	cs, _ = svc.Review(ctx, r2, cs.ID, DecisionRequestChanges, cs.SubmittedDigest, "改一下")
	if cs.State != StateDraft {
		t.Fatalf("request_changes 应回草稿: %s", cs.State)
	}
	for _, rv := range cs.Reviews {
		if !rv.Superseded {
			t.Fatal("退回后旧审核应作废")
		}
	}
	cs, _ = svc.Submit(ctx, author, cs.ID)
	cs, _ = svc.Review(ctx, r1, cs.ID, DecisionApprove, cs.SubmittedDigest, "")
	cs, _ = svc.Review(ctx, r2, cs.ID, DecisionApprove, cs.SubmittedDigest, "")
	if cs.State != StateApproved {
		t.Fatalf("两人批准应通过: %s", cs.State)
	}
	if _, err := svc.Review(ctx, r2, cs.ID, DecisionReject, cs.SubmittedDigest, ""); !errors.Is(err, ErrState) {
		t.Fatal("已批准不能再审")
	}
}

func TestFastTrackAndValidation(t *testing.T) {
	f := newFake()
	svc := NewService(f, fakePolicy{1}, nil)
	ctx := context.Background()
	member := Actor{UserID: "u-m", OrgID: "org", Subject: subject("u-m", identity.RoleMember, false)}
	admin := Actor{UserID: "u-a", OrgID: "org", Subject: subject("u-a", identity.RoleAdmin, true)}

	// 普通成员 fast_track 提交被拒
	cs, err := svc.Create(ctx, member, Input{Title: "t", FastTrack: true, Ops: []Op{putOp(f, "a", "# a")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Submit(ctx, member, cs.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member 不能 fast_track: %v", err)
	}
	// admin fast_track 直接 approved
	cs, _ = svc.Create(ctx, admin, Input{Title: "t", FastTrack: true, Ops: []Op{putOp(f, "b", "# b")}})
	cs, err = svc.Submit(ctx, admin, cs.ID)
	if err != nil || cs.State != StateApproved {
		t.Fatalf("admin fast_track: %v %s", err, cs.State)
	}

	// 校验：未上传的 blob、非法名字、不存在的落点、删除不存在的、空标题、入口内容非法
	bad := []Input{
		{Title: "t", Ops: []Op{{Op: OpPut, Level: resources.LevelProject, ProjectID: "p1", Kind: resources.KindRule, Name: "x",
			Files: []resources.FileRef{{Path: "RULE.md", SHA256: "sha256:nope", Size: 1}}}}},
		{Title: "t", Ops: []Op{{Op: OpPut, Level: resources.LevelProject, ProjectID: "p1", Kind: resources.KindRule, Name: "Bad Name"}}},
		{Title: "t", Ops: []Op{{Op: OpPut, Level: resources.LevelProject, ProjectID: "p-none", Kind: resources.KindRule, Name: "x"}}},
		{Title: "t", Ops: []Op{{Op: OpDelete, Level: resources.LevelProject, ProjectID: "p1", Kind: resources.KindRule, Name: "ghost"}}},
		{Title: " ", Ops: []Op{putOp(f, "a", "# a")}},
	}
	for i, in := range bad {
		if _, err := svc.Create(ctx, admin, in); err == nil {
			t.Fatalf("用例 %d 应被拒绝", i)
		}
	}
	// agent 的 name 与资源名不一致
	sum := resources.BlobSum([]byte("name: other\ndescription: d\ninstructions: x\n"))
	f.blobs[sum] = []byte("name: other\ndescription: d\ninstructions: x\n")
	_, err = svc.Create(ctx, admin, Input{Title: "t", Ops: []Op{{Op: OpPut, Level: resources.LevelOrg, Kind: resources.KindAgent, Name: "reviewer",
		Files: []resources.FileRef{{Path: "AGENT.yaml", SHA256: sum, Size: 10}}}}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("入口内容校验应在创建时失败: %v", err)
	}
}

func TestDigest_IsOrderIndependent(t *testing.T) {
	a := Op{Op: OpPut, Level: resources.LevelOrg, Kind: resources.KindRule, Name: "b", Files: []resources.FileRef{{Path: "RULE.md", SHA256: "s", Size: 1}}}
	b := Op{Op: OpDelete, Level: resources.LevelOrg, Kind: resources.KindRule, Name: "a"}
	if Digest([]Op{a, b}) != Digest([]Op{b, a}) {
		t.Fatal("摘要不应依赖操作顺序")
	}
	c := a
	c.Files = []resources.FileRef{{Path: "RULE.md", SHA256: "t", Size: 1}} // 不共享底层切片
	if Digest([]Op{a, b}) == Digest([]Op{c, b}) {
		t.Fatal("内容变化摘要必须变")
	}
}
