package api_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ith5/ith5/internal/api"
	"github.com/ith5/ith5/internal/resources"
	"github.com/ith5/ith5/internal/sync"
)

// upload 上传一段内容，返回其 FileRef。
func (e *env) upload(token, path, content string) resources.FileRef {
	e.t.Helper()
	sha := resources.BlobSum([]byte(content))
	req := httptest.NewRequest(http.MethodPost, "/v1/blobs", bytes.NewBufferString(content))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Sha256", sha)
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	e.must(rec, http.StatusCreated, "upload blob")
	return resources.FileRef{Path: path, SHA256: sha, Size: len(content)}
}

func ruleOp(name string, f resources.FileRef, projectID string) api.OpJSON {
	op := api.OpJSON{Op: "put", Level: "org", Kind: "rule", Name: name, Files: []resources.FileRef{f}}
	if projectID != "" {
		op.Level, op.ProjectID = "project", projectID
	}
	return op
}

func (e *env) snapshotFor(device, bindingID string) sync.Snapshot {
	e.t.Helper()
	rec, _ := e.do(http.MethodGet, "/v1/bindings/"+bindingID+"/snapshot", device, nil)
	e.must(rec, http.StatusOK, "snapshot")
	var snap sync.Snapshot
	decode(e.t, rec.Body.Bytes(), &snap)
	return snap
}

func TestIntegration_ChangesetReviewPublishRollback(t *testing.T) {
	e := newEnv(t)
	billing := e.projectID(e.member, "billing")

	// member 上传内容、建变更集、提交；member 不能 fast_track
	f1 := e.upload(e.member, "RULE.md", "# 新规范 v1\n")
	rec, cs := e.do(http.MethodPost, "/v1/change-sets", e.member, api.ChangesetReq{
		Title: "新增命名规范", Ops: []api.OpJSON{ruleOp("naming", f1, billing)},
	})
	e.must(rec, http.StatusCreated, "create changeset")
	csID := cs["id"].(string)
	if cs["state"] != "draft" {
		t.Fatalf("应为 draft: %v", cs["state"])
	}
	// 引用未上传的内容被拒
	rec, _ = e.do(http.MethodPost, "/v1/change-sets", e.member, api.ChangesetReq{
		Title: "bad", Ops: []api.OpJSON{ruleOp("x", resources.FileRef{Path: "RULE.md", SHA256: "sha256:" + repeat("1", 64), Size: 1}, billing)},
	})
	e.must(rec, http.StatusUnprocessableEntity, "missing blob")

	rec, diff := e.do(http.MethodGet, "/v1/change-sets/"+csID+"/diff", e.member, nil)
	e.must(rec, http.StatusOK, "diff")
	if diff["items"].([]any)[0].(map[string]any)["change"] != "create" {
		t.Fatalf("diff 应为 create: %v", diff)
	}

	rec, cs = e.do(http.MethodPost, "/v1/change-sets/"+csID+"/submit", e.member, nil)
	e.must(rec, http.StatusOK, "submit")
	digest := cs["submitted_digest"].(string)
	if cs["state"] != "in_review" || digest == "" {
		t.Fatalf("submit 后应 in_review: %v", cs)
	}

	// member 自审 403；owner 审核 digest 错 412；owner 批准 → approved
	rec, _ = e.do(http.MethodPost, "/v1/change-sets/"+csID+"/reviews", e.member, api.ReviewReq{Decision: "approve", Digest: digest})
	e.must(rec, http.StatusForbidden, "self review")
	rec, _ = e.do(http.MethodPost, "/v1/change-sets/"+csID+"/reviews", e.owner, api.ReviewReq{Decision: "approve", Digest: "sha256:stale"})
	e.must(rec, http.StatusPreconditionFailed, "stale digest")
	rec, cs = e.do(http.MethodPost, "/v1/change-sets/"+csID+"/reviews", e.owner, api.ReviewReq{Decision: "approve", Digest: digest, Comment: "ok"})
	e.must(rec, http.StatusOK, "approve")
	if cs["state"] != "approved" {
		t.Fatalf("应 approved: %v", cs["state"])
	}
	// 待审列表里 owner 能看到（此时已批准所以不在 review 视图），member 的 mine 视图能看到
	rec, mine := e.do(http.MethodGet, "/v1/change-sets?view=mine", e.member, nil)
	e.must(rec, http.StatusOK, "mine")
	if len(mine["items"].([]any)) != 1 {
		t.Fatalf("mine 应有 1 条: %v", mine)
	}

	// member 不能发布；owner 发布
	rec, _ = e.do(http.MethodPost, "/v1/change-sets/"+csID+"/publish", e.member, nil)
	e.must(rec, http.StatusForbidden, "member publish")
	rec, rel := e.do(http.MethodPost, "/v1/change-sets/"+csID+"/publish", e.owner, nil)
	e.must(rec, http.StatusOK, "publish")
	relID := rel["id"].(string)
	items := rel["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["version"].(float64) != 1 {
		t.Fatalf("release items: %v", items)
	}
	rec, _ = e.do(http.MethodPost, "/v1/change-sets/"+csID+"/publish", e.owner, nil)
	e.must(rec, http.StatusConflict, "重复发布")

	// 发布后 snapshot 里能看到新 rule
	device, _ := e.deviceFor(e.member)
	rec, b := e.do(http.MethodPost, "/v1/bindings", device, api.BindingReq{WorkspaceID: "ws3", ProjectIDs: []string{billing}})
	e.must(rec, http.StatusCreated, "bind")
	bindingID := b["id"].(string)
	snap := e.snapshotFor(device, bindingID)
	found := false
	for _, r := range snap.Resources {
		if r.Kind == resources.KindRule && r.Name == "naming" && r.Version == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("发布后 snapshot 应含新 rule")
	}
	rev1 := snap.Revision

	// 第二个变更集：更新同一 rule；fast_track 由 owner 直接发布
	f2 := e.upload(e.owner, "RULE.md", "# 新规范 v2\n")
	rec, cs2 := e.do(http.MethodPost, "/v1/change-sets", e.owner, api.ChangesetReq{
		Title: "更新命名规范", FastTrack: true, Ops: []api.OpJSON{ruleOp("naming", f2, billing)},
	})
	e.must(rec, http.StatusCreated, "create v2")
	cs2ID := cs2["id"].(string)
	rec, cs2 = e.do(http.MethodPost, "/v1/change-sets/"+cs2ID+"/submit", e.owner, nil)
	e.must(rec, http.StatusOK, "submit fast_track")
	if cs2["state"] != "approved" {
		t.Fatalf("fast_track 提交后应直接 approved: %v", cs2["state"])
	}
	rec, rel2 := e.do(http.MethodPost, "/v1/change-sets/"+cs2ID+"/publish", e.owner, nil)
	e.must(rec, http.StatusOK, "publish v2")
	if rel2["items"].([]any)[0].(map[string]any)["version"].(float64) != 2 {
		t.Fatalf("应为版本 2: %v", rel2)
	}
	snap = e.snapshotFor(device, bindingID)
	if snap.Revision == rev1 {
		t.Fatal("发布后 revision 应变化")
	}

	// 基线冲突：cs3 在 v2 发布前创建（基线是 v1 之后的 head），操作同一 rule → 412；操作另一个 rule → 自动放行
	// 先用 API 构造：创建 cs3 与 cs4（此时 head 已是 v2），再发布一个 cs5 改 naming，之后发布 cs3 应 412、cs4 应成功
	f3 := e.upload(e.owner, "RULE.md", "# v3\n")
	rec, cs3 := e.do(http.MethodPost, "/v1/change-sets", e.owner, api.ChangesetReq{Title: "cs3", FastTrack: true, Ops: []api.OpJSON{ruleOp("naming", f3, billing)}})
	e.must(rec, http.StatusCreated, "cs3")
	f4 := e.upload(e.owner, "RULE.md", "# other\n")
	rec, cs4 := e.do(http.MethodPost, "/v1/change-sets", e.owner, api.ChangesetReq{Title: "cs4", FastTrack: true, Ops: []api.OpJSON{ruleOp("other", f4, billing)}})
	e.must(rec, http.StatusCreated, "cs4")
	f5 := e.upload(e.owner, "RULE.md", "# v3-by-cs5\n")
	rec, cs5 := e.do(http.MethodPost, "/v1/change-sets", e.owner, api.ChangesetReq{Title: "cs5", FastTrack: true, Ops: []api.OpJSON{ruleOp("naming", f5, billing)}})
	e.must(rec, http.StatusCreated, "cs5")
	for _, id := range []string{cs5["id"].(string)} {
		rec, _ = e.do(http.MethodPost, "/v1/change-sets/"+id+"/submit", e.owner, nil)
		e.must(rec, http.StatusOK, "submit cs5")
		rec, _ = e.do(http.MethodPost, "/v1/change-sets/"+id+"/publish", e.owner, nil)
		e.must(rec, http.StatusOK, "publish cs5")
	}
	rec, _ = e.do(http.MethodPost, "/v1/change-sets/"+cs3["id"].(string)+"/submit", e.owner, nil)
	e.must(rec, http.StatusOK, "submit cs3")
	rec, body := e.do(http.MethodPost, "/v1/change-sets/"+cs3["id"].(string)+"/publish", e.owner, nil)
	e.must(rec, http.StatusPreconditionFailed, "cs3 应基线冲突")
	if errCodeOf(body) != "REVISION_MISMATCH" {
		t.Fatalf("错误码: %v", body)
	}
	rec, _ = e.do(http.MethodPost, "/v1/change-sets/"+cs4["id"].(string)+"/submit", e.owner, nil)
	e.must(rec, http.StatusOK, "submit cs4")
	rec, _ = e.do(http.MethodPost, "/v1/change-sets/"+cs4["id"].(string)+"/publish", e.owner, nil)
	e.must(rec, http.StatusOK, "cs4 不重叠应自动放行")

	// 回滚 rel（v1 的发布）：naming 之前不存在 → 回滚 = 删除；发布后 snapshot 里没有 naming
	rec, rb := e.do(http.MethodPost, "/v1/releases/"+relID+"/rollback", e.owner, api.RollbackReq{FastTrack: true})
	e.must(rec, http.StatusCreated, "rollback changeset")
	rbID := rb["id"].(string)
	if rb["ops"].([]any)[0].(map[string]any)["op"] != "delete" {
		t.Fatalf("首版回滚应是删除: %v", rb["ops"])
	}
	rec, _ = e.do(http.MethodPost, "/v1/change-sets/"+rbID+"/submit", e.owner, nil)
	e.must(rec, http.StatusOK, "submit rollback")
	rec, _ = e.do(http.MethodPost, "/v1/change-sets/"+rbID+"/publish", e.owner, nil)
	e.must(rec, http.StatusOK, "publish rollback")
	snap = e.snapshotFor(device, bindingID)
	for _, r := range snap.Resources {
		if r.Kind == resources.KindRule && r.Name == "naming" {
			t.Fatal("回滚（删除）后 snapshot 不应再有 naming")
		}
	}

	// 资源读：后台能看到 tombstone 版本与历史
	rec, list := e.do(http.MethodGet, "/v1/resources?kind=rule&q=naming", e.owner, nil)
	e.must(rec, http.StatusOK, "list resources")
	var resID string
	for _, it := range list["items"].([]any) {
		m := it.(map[string]any)
		if m["name"] == "naming" && m["level"] == "project" {
			resID = m["id"].(string)
			if m["deleted"] != true || m["version"].(float64) != 4 {
				t.Fatalf("当前版本应是 tombstone v4: %v", m)
			}
		}
	}
	rec, detail := e.do(http.MethodGet, "/v1/resources/"+resID, e.owner, nil)
	e.must(rec, http.StatusOK, "get resource")
	versions := detail["versions"].([]any)
	if len(versions) != 4 {
		t.Fatalf("应有 4 个版本: %d", len(versions))
	}
	v1 := versions[3].(map[string]any)
	rec, vbody := e.do(http.MethodGet, "/v1/resource-versions/"+v1["id"].(string), e.owner, nil)
	e.must(rec, http.StatusOK, "get version")
	if vbody["contents"].(map[string]any)["RULE.md"] != "# 新规范 v1\n" {
		t.Fatalf("版本内容应内联: %v", vbody)
	}
	// 发布历史
	rec, rels := e.do(http.MethodGet, "/v1/releases?project_id="+billing, e.owner, nil)
	e.must(rec, http.StatusOK, "list releases")
	if len(rels["items"].([]any)) != 5 {
		t.Fatalf("billing 应有 5 次发布: %d", len(rels["items"].([]any)))
	}
	// 审计里有发布记录
	rec, au := e.do(http.MethodGet, "/v1/audit/events?type=release.", e.owner, nil)
	e.must(rec, http.StatusOK, "audit")
	if len(au["items"].([]any)) < 5 {
		t.Fatalf("审计应记录发布: %d", len(au["items"].([]any)))
	}
}

func TestIntegration_ChangesetPermissionsAndTags(t *testing.T) {
	e := newEnv(t)
	website := e.projectID(e.owner, "website")
	// member 不是 website 成员：不能对它提变更集
	f := e.upload(e.member, "RULE.md", "# x\n")
	rec, _ := e.do(http.MethodPost, "/v1/change-sets", e.member, api.ChangesetReq{Title: "t", Ops: []api.OpJSON{ruleOp("x", f, website)}})
	e.must(rec, http.StatusForbidden, "非成员提变更集")
	// org 级：member 能提
	rec, cs := e.do(http.MethodPost, "/v1/change-sets", e.member, api.ChangesetReq{Title: "t", Ops: []api.OpJSON{ruleOp("org-rule", f, "")}})
	e.must(rec, http.StatusCreated, "org 级提变更集")
	// owner 看不到未提交的草稿以外的他人内容？作者与有读权限者可见：owner 是 admin 可见
	rec, _ = e.do(http.MethodGet, "/v1/change-sets/"+cs["id"].(string), e.owner, nil)
	e.must(rec, http.StatusOK, "owner 读")
	// 作者取消
	rec, _ = e.do(http.MethodPost, "/v1/change-sets/"+cs["id"].(string)+"/cancel", e.member, nil)
	e.must(rec, http.StatusOK, "cancel")
	rec, _ = e.do(http.MethodPost, "/v1/change-sets/"+cs["id"].(string)+"/submit", e.member, nil)
	e.must(rec, http.StatusConflict, "取消后不能提交")

	// 标签：只有发布权限的人能改
	rec, list := e.do(http.MethodGet, "/v1/resources?kind=skill&q=corp-common", e.owner, nil)
	e.must(rec, http.StatusOK, "list")
	id := list["items"].([]any)[0].(map[string]any)["id"].(string)
	rec, _ = e.do(http.MethodPut, "/v1/resources/"+id+"/tags", e.member, api.TagsReq{Tags: []string{"a"}})
	e.must(rec, http.StatusForbidden, "member 改标签")
	rec, r := e.do(http.MethodPut, "/v1/resources/"+id+"/tags", e.owner, api.TagsReq{Tags: []string{"a", "b"}})
	e.must(rec, http.StatusOK, "owner 改标签")
	if len(r["tags"].([]any)) != 2 {
		t.Fatalf("tags: %v", r)
	}
	rec, _ = e.do(http.MethodPut, "/v1/resources/"+id+"/tags", e.owner, api.TagsReq{Tags: []string{"bad tag"}})
	e.must(rec, http.StatusUnprocessableEntity, "非法标签")
}

func errCodeOf(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}
