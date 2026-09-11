package resources

import (
	"sort"
	"time"
)

// Resource 是一条资源的元数据，不含正文。
type Resource struct {
	ID          string
	OrgID       string
	Level       Level
	OwnerID     string // org 级为 org_id，team 级为 team_id，project 级为 project_id
	Kind        Kind
	Name        string
	Description string
	Tags        []string
	Archived    bool
	// Version 是当前已发布版本；0 表示尚无版本。
	Version  int
	Checksum string
	// Deleted 表示当前版本是 tombstone。
	Deleted bool
}

// SubjectType 是授权主体类型。
//
// 刻意不含 role：组织角色管的是「能不能进后台」，把它当授权维度会被误读成岗位；
// 岗位那一层由权限组承担。
type SubjectType string

const (
	SubjectOrg     SubjectType = "org"
	SubjectUser    SubjectType = "user"
	SubjectProject SubjectType = "project"
)

// PermissionGroup 是一组资源的具名集合，授权给人或项目（docs/设计-资源类型与层级.md §5）。
type PermissionGroup struct {
	ID          string
	OrgID       string
	Key         string
	Name        string
	Archived    bool
	ResourceIDs []string
}

// Assignment 是一条授权：目标是单个资源或一个权限组，主体是组织、用户或项目。
type Assignment struct {
	OrgID       string
	ResourceID  string // 与 GroupID 二选一
	GroupID     string
	SubjectType SubjectType
	SubjectID   string // SubjectOrg 时为空
	ExpiresAt   *time.Time
}

// GrantSource 说明某资源是「凭什么」拿到的。
//
// 管理员最常问的两个问题——「他为什么能拿到这个」和「我授权了他为什么没有」——
// 没有这个结构就答不了。
type GrantSource struct {
	SubjectType SubjectType
	SubjectID   string
	GroupID     string
	GroupKey    string
	GroupName   string
}

// Grant 是解析结果的一项：一个资源加上它的全部授权来源。
type Grant struct {
	Resource Resource
	Via      []GrantSource
}

// Principal 是发起请求的人及其归属。
type Principal struct {
	UserID     string
	OrgID      string
	Suspended  bool
	ProjectIDs []string
}

// ResolveGrants 计算 principal 经权限组与直接授权拿到的资源。
//
// 这只是解析的第 4 步（权限组叠加）；层级落点的解析在 sync 包。
//
// 规则：
//   - suspended 用户返回空集合
//   - 结果是并集，没有 deny
//   - ExpiresAt 为 nil 或严格晚于 now 才有效
//   - archived 的组与资源、无已发布版本的资源一律排除
//   - project 级资源还必须属于 principal 参与的项目
//   - 跨 org 的资源与授权一律跳过（纵深防御：db 层本就应按 org 过滤，这里再挡一道）
//   - 输出按 Kind、Name 稳定排序
//
// 时间基准由调用方传入，服务端一律传 now()，不接受客户端时间。
func ResolveGrants(p Principal, all []Resource, groups []PermissionGroup, assigns []Assignment, now time.Time) []Grant {
	if p.Suspended {
		return []Grant{}
	}
	projects := make(map[string]bool, len(p.ProjectIDs))
	for _, id := range p.ProjectIDs {
		projects[id] = true
	}
	groupByID := make(map[string]PermissionGroup, len(groups))
	for _, g := range groups {
		if g.OrgID != p.OrgID || g.Archived {
			continue
		}
		groupByID[g.ID] = g
	}

	via := make(map[string][]GrantSource)
	for _, a := range assigns {
		if a.OrgID != p.OrgID || !assignmentActive(a, now) || !subjectMatches(a, p, projects) {
			continue
		}
		src := GrantSource{SubjectType: a.SubjectType, SubjectID: a.SubjectID}
		switch {
		case a.ResourceID != "":
			via[a.ResourceID] = append(via[a.ResourceID], src)
		case a.GroupID != "":
			g, ok := groupByID[a.GroupID]
			if !ok {
				continue
			}
			src.GroupID, src.GroupKey, src.GroupName = g.ID, g.Key, g.Name
			for _, rid := range g.ResourceIDs {
				via[rid] = append(via[rid], src)
			}
		}
	}

	out := make([]Grant, 0, len(via))
	for _, r := range all {
		sources, ok := via[r.ID]
		if !ok || r.OrgID != p.OrgID || r.Archived || r.Version <= 0 || r.Deleted {
			continue
		}
		if r.Level == LevelProject && !projects[r.OwnerID] {
			continue
		}
		out = append(out, Grant{Resource: r, Via: sources})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Resource.Kind != out[j].Resource.Kind {
			return out[i].Resource.Kind < out[j].Resource.Kind
		}
		return out[i].Resource.Name < out[j].Resource.Name
	})
	return out
}

// assignmentActive 判断授权是否在有效期内。语义为闭区间到期：expires_at 到达的那一刻立即失效。
func assignmentActive(a Assignment, now time.Time) bool {
	return a.ExpiresAt == nil || a.ExpiresAt.After(now)
}

func subjectMatches(a Assignment, p Principal, projects map[string]bool) bool {
	switch a.SubjectType {
	case SubjectOrg:
		return true
	case SubjectUser:
		return a.SubjectID == p.UserID
	case SubjectProject:
		return projects[a.SubjectID]
	default:
		return false // 未知主体类型一律不授权，宁可少给不可多给
	}
}
