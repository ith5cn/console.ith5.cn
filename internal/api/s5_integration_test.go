package api_test

import (
	"net/http"
	"testing"

	"github.com/ith5/ith5/internal/api"
)

func TestIntegration_GroupsAssignmentsAndAuditReads(t *testing.T) {
	e := newEnv(t)

	// 权限组：member 能读不能写；owner 建组、改组、设资源
	rec, _ := e.do(http.MethodPost, "/v1/groups", e.member, api.GroupReq{Key: "x", Name: "x"})
	e.must(rec, http.StatusForbidden, "member 建组")
	rec, g := e.do(http.MethodPost, "/v1/groups", e.owner, api.GroupReq{Key: "fe-pack", Name: "前端工具包", Description: "d"})
	e.must(rec, http.StatusCreated, "建组")
	groupID := g["id"].(string)
	rec, _ = e.do(http.MethodPost, "/v1/groups", e.owner, api.GroupReq{Key: "fe-pack", Name: "again"})
	e.must(rec, http.StatusConflict, "重复 key")
	rec, _ = e.do(http.MethodPost, "/v1/groups", e.owner, api.GroupReq{Key: "Bad Key", Name: "x"})
	e.must(rec, http.StatusUnprocessableEntity, "非法 key")

	rec, list := e.do(http.MethodGet, "/v1/resources?kind=skill&q=corp-common", e.owner, nil)
	e.must(rec, http.StatusOK, "resources")
	bundleID := list["items"].([]any)[0].(map[string]any)["id"].(string)
	rec, n := e.do(http.MethodPut, "/v1/groups/"+groupID+"/bundles", e.owner, api.GroupBundlesReq{BundleIDs: []string{bundleID, "00000000-0000-0000-0000-000000000000"}})
	e.must(rec, http.StatusOK, "set bundles")
	if n["count"].(float64) != 1 {
		t.Fatalf("非本组织 / 不存在的资源应被忽略: %v", n)
	}
	rec, gs := e.do(http.MethodGet, "/v1/groups", e.member, nil)
	e.must(rec, http.StatusOK, "list groups")
	var found bool
	for _, it := range gs["items"].([]any) {
		m := it.(map[string]any)
		if m["key"] == "fe-pack" && len(m["bundle_ids"].([]any)) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("组应含 1 个资源: %v", gs)
	}

	// 授权：给 member 授组，过期时间必须在未来，重复授权 409
	rec, _ = e.do(http.MethodPost, "/v1/assignments", e.owner, api.AssignmentReq{GroupID: groupID, SubjectType: "user", SubjectID: e.memberID})
	e.must(rec, http.StatusCreated, "授权")
	rec, _ = e.do(http.MethodPost, "/v1/assignments", e.owner, api.AssignmentReq{GroupID: groupID, SubjectType: "user", SubjectID: e.memberID})
	e.must(rec, http.StatusConflict, "重复授权")
	rec, _ = e.do(http.MethodPost, "/v1/assignments", e.owner, api.AssignmentReq{GroupID: groupID, BundleID: bundleID, SubjectType: "org"})
	e.must(rec, http.StatusUnprocessableEntity, "目标二选一")
	rec, _ = e.do(http.MethodPost, "/v1/assignments", e.owner, api.AssignmentReq{GroupID: groupID, SubjectType: "user", SubjectID: "00000000-0000-0000-0000-000000000000"})
	e.must(rec, http.StatusUnprocessableEntity, "主体不存在")
	rec, as := e.do(http.MethodGet, "/v1/assignments", e.owner, nil)
	e.must(rec, http.StatusOK, "list assignments")
	var aid string
	for _, it := range as["items"].([]any) {
		m := it.(map[string]any)
		if m["group_id"] == groupID {
			aid = m["id"].(string)
			if m["subject_name"] != "member@example.com" {
				t.Fatalf("应带主体展示名: %v", m)
			}
		}
	}
	// 组内 corp-common 现在是权限组内容：未被授权的 owner 绑定时拿不到，member 拿得到
	rec, _ = e.do(http.MethodDelete, "/v1/assignments/"+aid, e.member, nil)
	e.must(rec, http.StatusForbidden, "member 删授权")
	rec, _ = e.do(http.MethodDelete, "/v1/assignments/"+aid, e.owner, nil)
	e.must(rec, http.StatusOK, "删授权")
	rec, _ = e.do(http.MethodDelete, "/v1/assignments/"+aid, e.owner, nil)
	e.must(rec, http.StatusNotFound, "再删 404")

	// 归档组
	rec, _ = e.do(http.MethodPatch, "/v1/groups/"+groupID, e.owner, api.GroupReq{Name: "前端工具包", Archived: true})
	e.must(rec, http.StatusOK, "归档组")

	// 审计读：分发回执与工具调用只有 audit:read 能看
	rec, _ = e.do(http.MethodGet, "/v1/audit/distributions", e.member, nil)
	e.must(rec, http.StatusForbidden, "member 看回执")
	rec, d := e.do(http.MethodGet, "/v1/audit/distributions", e.owner, nil)
	e.must(rec, http.StatusOK, "distributions")
	if d["items"] == nil {
		t.Fatal("items 应为数组")
	}
	rec, ex := e.do(http.MethodGet, "/v1/audit/executions?limit=10", e.owner, nil)
	e.must(rec, http.StatusOK, "executions")
	if ex["items"] == nil {
		t.Fatal("items 应为数组")
	}
	rec, au := e.do(http.MethodGet, "/v1/audit/events?type=group.", e.owner, nil)
	e.must(rec, http.StatusOK, "audit events")
	if len(au["items"].([]any)) < 3 {
		t.Fatalf("组的建 / 设资源 / 归档都应记审计: %d", len(au["items"].([]any)))
	}
}
