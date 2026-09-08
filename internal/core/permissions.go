package core

import (
	"sort"
	"time"
)

// Resolve 计算某个 principal 有权拿到的 Bundle 及其授权来源（技术方案 §7.2）。
//
// 两步：
//  1. 匹配 subject 且未过期的 Assignment
//  2. 展开：指向权限组的取组内全部 bundle，指向单个 bundle 的直接取
//
// 过滤规则：
//   - suspended 用户返回空集合（D4：manifest 返回空，使 sync 能执行撤销）
//   - 结果是并集，没有 deny 规则
//   - ExpiresAt 为 nil 或**严格晚于** now 才有效（闭区间到期）
//   - archived 的权限组与 Bundle 一律排除
//   - 无已发布版本（Version <= 0）的 Bundle 一律排除
//   - scope=project 的 Bundle 还必须属于 principal 参与的项目
//   - **跨 org 的 Bundle 与 Assignment 一律跳过**（纵深防御，见下）
//   - 输出按 Kind、Name 稳定排序
//
// 时间基准由调用方传入，服务端一律传 now()，不接受客户端时间。
//
// 关于 org 校验：db 层本就应按 org_id 过滤（技术方案 §2 原则 8），
// 这里再挡一道。多一个字段比较，换掉一整类「某天忘了加 WHERE」的跨租户泄露。
func Resolve(
	p Principal,
	bundles []BundleMeta,
	groups []PermissionGroup,
	assigns []Assignment,
	now time.Time,
) []Grant {
	if p.Suspended {
		return []Grant{}
	}

	projects := make(map[string]bool, len(p.ProjectIDs))
	for _, id := range p.ProjectIDs {
		projects[id] = true
	}

	// 权限组索引，跨 org 与已归档的组直接排除。
	groupByID := make(map[string]PermissionGroup, len(groups))
	for _, g := range groups {
		if g.OrgID != p.OrgID || g.Archived {
			continue
		}
		groupByID[g.ID] = g
	}

	// bundleID -> 全部授权来源。同一 bundle 可经多条路径拿到，全部记录。
	via := make(map[string][]GrantSource)
	for _, a := range assigns {
		if a.OrgID != p.OrgID {
			continue
		}
		if !assignmentActive(a, now) || !subjectMatches(a, p, projects) {
			continue
		}
		src := GrantSource{SubjectType: a.SubjectType, SubjectID: a.SubjectID}

		switch {
		case a.BundleID != "":
			via[a.BundleID] = append(via[a.BundleID], src)
		case a.GroupID != "":
			g, ok := groupByID[a.GroupID]
			if !ok {
				continue // 组不存在、已归档或跨 org
			}
			src.GroupID = g.ID
			src.GroupName = g.Name
			for _, bid := range g.BundleIDs {
				via[bid] = append(via[bid], src)
			}
		}
		// 两者皆空的 Assignment 是脏数据，DB 的 CHECK 已拦住；此处静默忽略。
	}

	out := make([]Grant, 0, len(via))
	for _, b := range bundles {
		sources, ok := via[b.ID]
		if !ok {
			continue
		}
		if b.OrgID != p.OrgID {
			continue
		}
		if b.Archived || b.Version <= 0 {
			continue
		}
		// scope=project 的 Bundle 必须与 principal 的项目归属一致，
		// 避免仅凭一条 org 级授权就拿到别的项目的内容。
		if b.Scope == ScopeProject && !projects[b.ProjectID] {
			continue
		}
		out = append(out, Grant{Bundle: b, Via: sources})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Bundle.Kind != out[j].Bundle.Kind {
			return out[i].Bundle.Kind < out[j].Bundle.Kind
		}
		return out[i].Bundle.Name < out[j].Bundle.Name
	})
	return out
}

// Bundles 从解析结果中取出 Bundle 列表，供 manifest 直接使用。
func Bundles(grants []Grant) []BundleMeta {
	out := make([]BundleMeta, len(grants))
	for i, g := range grants {
		out[i] = g.Bundle
	}
	return out
}

// assignmentActive 判断授权是否在有效期内。
// 语义为闭区间到期：expires_at 到达的那一刻立即失效。
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
		// 未知 subject 类型一律不授权，宁可少给不可多给。
		return false
	}
}
