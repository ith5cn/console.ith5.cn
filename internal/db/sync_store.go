package db

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ith5/ith5/internal/platform/pg"
	"github.com/ith5/ith5/internal/projects"
	"github.com/ith5/ith5/internal/resources"
	"github.com/ith5/ith5/internal/sync"
)

// SyncStore 实现 sync.Store。
type SyncStore struct{ db *DB }

// Sync 返回同步模块的仓储。
func (d *DB) Sync() *SyncStore { return &SyncStore{db: d} }

var _ sync.Store = (*SyncStore)(nil)

// candidateSelect 取每个资源的当前版本（版本号最大的那条），含 tombstone。
// policy / culture 顺带取出入口文件正文，Resolve 需要它做合并。
const candidateSelect = `
	WITH cur AS (
		SELECT DISTINCT ON (bundle_id) bundle_id, id, version, files, checksum, deleted
		FROM bundle_versions ORDER BY bundle_id, version DESC
	)
	SELECT b.id::text, b.org_id::text, b.level::text, b.owner_id::text, b.kind::text, b.name, b.description,
	       b.tags, b.archived, cur.id::text, cur.version, cur.files, cur.checksum, cur.deleted,
	       coalesce(p.slug, ''), coalesce(t.slug, ''),
	       CASE WHEN b.kind IN ('policy', 'culture') THEN
	         (SELECT convert_from(bl.bytes, 'UTF8') FROM blobs bl
	           WHERE bl.org_id = b.org_id AND bl.sha256 = (cur.files->0->>'sha256'))
	       ELSE '' END
	FROM bundles b
	JOIN cur ON cur.bundle_id = b.id
	LEFT JOIN projects p ON p.id = b.project_id
	LEFT JOIN teams t ON t.id = b.team_id`

func scanCandidate(rows pgx.Rows) (resources.Candidate, error) {
	var c resources.Candidate
	var level, kind, projectSlug, teamSlug string
	var files []byte
	err := rows.Scan(&c.ID, &c.OrgID, &level, &c.OwnerID, &kind, &c.Name, &c.Description,
		&c.Tags, &c.Archived, &c.VersionID, &c.Version, &files, &c.Checksum, &c.Deleted,
		&projectSlug, &teamSlug, &c.Content)
	if err != nil {
		return c, err
	}
	c.Level, c.Kind = resources.Level(level), resources.Kind(kind)
	if err := json.Unmarshal(files, &c.Files); err != nil {
		return c, err
	}
	switch c.Level {
	case resources.LevelProject:
		c.Namespace = projectSlug
	case resources.LevelTeam:
		c.Namespace = teamSlug
	default:
		c.Namespace = "common"
	}
	return c, nil
}

// ListCandidates 返回按层级自动分发的资源。
//
// 属于任一权限组的资源不在其中：它们是「谁额外拿到」的内容，只经授权分发
// （docs/设计-资源类型与层级.md §5）。否则一个 org 级的权限组内容会发给全员，权限组就失去意义。
func (s *SyncStore) ListCandidates(ctx context.Context, orgID string) ([]resources.Candidate, error) {
	return s.listCandidates(ctx, orgID, true)
}

func (s *SyncStore) listCandidates(ctx context.Context, orgID string, excludeGrouped bool) ([]resources.Candidate, error) {
	rows, err := s.db.pool.Query(ctx, candidateSelect+`
		WHERE b.org_id = $1 AND NOT b.archived
		  AND (NOT $2 OR NOT EXISTS (SELECT 1 FROM permission_group_bundles gb WHERE gb.bundle_id = b.id))`, orgID, excludeGrouped)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []resources.Candidate{}
	for rows.Next() {
		c, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListGroupCandidates 复用 resources.ResolveGrants：先取组织内全部资源元数据、权限组与授权，
// 解析出经权限组拿到的资源，再补上文件清单。
func (s *SyncStore) ListGroupCandidates(ctx context.Context, orgID, userID string, projectIDs []string) ([]resources.Candidate, []sync.GrantRef, error) {
	all, err := s.listCandidates(ctx, orgID, false)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]resources.Candidate, len(all))
	metas := make([]resources.Resource, 0, len(all))
	for _, c := range all {
		byID[c.ID] = c
		metas = append(metas, c.Resource)
	}

	groups, err := s.listGroups(ctx, orgID)
	if err != nil {
		return nil, nil, err
	}
	assigns, err := s.listAssignments(ctx, orgID)
	if err != nil {
		return nil, nil, err
	}
	var suspended bool
	if err := s.db.pool.QueryRow(ctx, `SELECT status = 'suspended' FROM users WHERE id = $1`, userID).Scan(&suspended); err != nil {
		return nil, nil, err
	}
	grants := resources.ResolveGrants(resources.Principal{UserID: userID, OrgID: orgID, Suspended: suspended, ProjectIDs: projectIDs},
		metas, groups, assigns, time.Now())

	out := make([]resources.Candidate, 0, len(grants))
	seenGroup := map[string]sync.GrantRef{}
	for _, g := range grants {
		c := byID[g.Resource.ID]
		// 一个资源可经多条路径拿到；取第一条带权限组的作为命名空间，没有权限组的（直接授权）用 direct
		c.ViaGroup, c.Namespace = "direct", "direct"
		for _, v := range g.Via {
			if v.GroupID != "" {
				c.ViaGroup, c.Namespace = v.GroupKey, v.GroupKey
				seenGroup[v.GroupKey] = sync.GrantRef{GroupKey: v.GroupKey, Name: v.GroupName}
				break
			}
		}
		out = append(out, c)
	}
	refs := make([]sync.GrantRef, 0, len(seenGroup))
	for _, r := range seenGroup {
		refs = append(refs, r)
	}
	return out, refs, nil
}

func (s *SyncStore) listGroups(ctx context.Context, orgID string) ([]resources.PermissionGroup, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT g.id::text, g.org_id::text, g.key, g.name, g.archived,
		       coalesce(array_agg(gb.bundle_id::text) FILTER (WHERE gb.bundle_id IS NOT NULL), '{}')
		FROM permission_groups g LEFT JOIN permission_group_bundles gb ON gb.group_id = g.id
		WHERE g.org_id = $1 GROUP BY g.id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []resources.PermissionGroup
	for rows.Next() {
		var g resources.PermissionGroup
		if err := rows.Scan(&g.ID, &g.OrgID, &g.Key, &g.Name, &g.Archived, &g.ResourceIDs); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *SyncStore) listAssignments(ctx context.Context, orgID string) ([]resources.Assignment, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT org_id::text, coalesce(bundle_id::text, ''), coalesce(group_id::text, ''),
		       subject_type::text, coalesce(subject_id::text, ''), expires_at
		FROM assignments WHERE org_id = $1`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []resources.Assignment
	for rows.Next() {
		var a resources.Assignment
		var st string
		if err := rows.Scan(&a.OrgID, &a.ResourceID, &a.GroupID, &st, &a.SubjectID, &a.ExpiresAt); err != nil {
			return nil, err
		}
		a.SubjectType = resources.SubjectType(st)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *SyncStore) ProjectRefs(ctx context.Context, orgID string, projectIDs []string) ([]sync.ProjectRef, []string, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT p.id::text, p.slug, p.name, coalesce(p.team_id::text, ''), coalesce(h.revision, '')
		FROM projects p LEFT JOIN content_heads h ON h.level = 'project' AND h.owner_id = p.id
		WHERE p.org_id = $1 AND p.id = ANY($2::uuid[]) AND NOT p.archived ORDER BY p.slug`, orgID, projectIDs)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	refs := []sync.ProjectRef{}
	teams := map[string]bool{}
	for rows.Next() {
		var r sync.ProjectRef
		var teamID string
		if err := rows.Scan(&r.ID, &r.Slug, &r.Name, &teamID, &r.Revision); err != nil {
			return nil, nil, err
		}
		refs = append(refs, r)
		if teamID != "" {
			teams[teamID] = true
		}
	}
	teamIDs := make([]string, 0, len(teams))
	for id := range teams {
		teamIDs = append(teamIDs, id)
	}
	return refs, teamIDs, rows.Err()
}

func (s *SyncStore) OrgRef(ctx context.Context, orgID string) (sync.OrgRef, error) {
	var o sync.OrgRef
	err := s.db.pool.QueryRow(ctx, `SELECT id::text, slug, name FROM organizations WHERE id = $1`, orgID).Scan(&o.ID, &o.Slug, &o.Name)
	return o, err
}

// GetBlob 只放行「被用户可见的某个版本引用」的哈希：org 级资源，或用户所在团队 / 项目的资源，
// 或经权限组授予的资源。不区分不存在与无权，一律 ErrBlobNotVisible。
func (s *SyncStore) GetBlob(ctx context.Context, orgID, userID, sha string) ([]byte, error) {
	var b []byte
	err := s.db.pool.QueryRow(ctx, `
		SELECT bl.bytes FROM blobs bl
		WHERE bl.org_id = $1 AND bl.sha256 = $3
		  AND EXISTS (
		    SELECT 1 FROM bundle_versions v
		    JOIN bundles bd ON bd.id = v.bundle_id
		    WHERE bd.org_id = $1
		      AND v.files @> jsonb_build_array(jsonb_build_object('sha256', $3::text))
		      AND (
		        bd.level = 'org'
		        OR (bd.level = 'team' AND EXISTS (SELECT 1 FROM team_members tm WHERE tm.team_id = bd.team_id AND tm.user_id = $2))
		        OR (bd.level = 'project' AND EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = bd.project_id AND pm.user_id = $2))
		        OR (bd.level = 'project' AND EXISTS (
		              SELECT 1 FROM projects p JOIN team_members tm ON tm.team_id = p.team_id
		              WHERE p.id = bd.project_id AND tm.user_id = $2))
		        OR EXISTS (SELECT 1 FROM users u WHERE u.id = $2 AND u.role IN ('owner', 'admin'))
		      )
		  )`, orgID, userID, sha).Scan(&b)
	if isNoRows(err) {
		return nil, sync.ErrBlobNotVisible
	}
	return b, err
}

func (s *SyncStore) RecordResults(ctx context.Context, b projects.Binding, revision, ip string, results []sync.Result, now time.Time) error {
	return pg.WithTx(ctx, s.db.pool, func(tx pgx.Tx) error {
		for _, r := range results {
			detail, _ := json.Marshal(r.Detail)
			var ipArg any
			if ip != "" {
				ipArg = ip
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO distribution_logs (org_id, user_id, machine_id, binding_id, kind, name, action, detail, revision, ip, occurred_at)
				VALUES ($1, $2, $3, $4, $5::bundle_kind, $6, $7::distribution_action, $8, $9, $10, $11)`,
				b.OrgID, b.UserID, b.MachineID, b.ID, r.Kind, r.Name, r.Action, detail, revision, ipArg, now); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `
			UPDATE bindings SET applied_revision = $3, last_sync_at = $4, updated_at = clock_timestamp()
			WHERE org_id = $1 AND id = $2`, b.OrgID, b.ID, revision, now)
		return err
	})
}

// ---------------------------------------------------------------
// 直接写入已发布版本：seed 与 S3 之前的内容导入使用
// ---------------------------------------------------------------

// PutPublished 在一个事务里写入 blob、bundle 与新版本（版本号 = 当前最大 + 1）。
// 不经变更集，只给 seed 与测试用；正式路径在 S3 的 releases 包。
func (d *DB) PutPublished(ctx context.Context, orgID string, level resources.Level, ownerID string, kind resources.Kind, name, description string, tags []string, files []resources.File, publishedBy string) (string, error) {
	if err := resources.ValidateForPublish(kind, name, files); err != nil {
		return "", err
	}
	refs, blobs := resources.ToRefs(files)
	sum, err := resources.Checksum(refs)
	if err != nil {
		return "", err
	}
	refsJSON, _ := json.Marshal(refs)
	var bundleID string
	err = pg.WithTx(ctx, d.pool, func(tx pgx.Tx) error {
		for sha, content := range blobs {
			if _, err := tx.Exec(ctx, `
				INSERT INTO blobs (org_id, sha256, bytes, size) VALUES ($1, $2, $3, $4)
				ON CONFLICT (org_id, sha256) DO NOTHING`, orgID, sha, content, len(content)); err != nil {
				return err
			}
		}
		teamID, projectID := "", ""
		switch level {
		case resources.LevelTeam:
			teamID = ownerID
		case resources.LevelProject:
			projectID = ownerID
		}
		if tags == nil {
			tags = []string{}
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO bundles (org_id, level, team_id, project_id, kind, name, description, tags)
			VALUES ($1, $2::owner_level, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, $5::bundle_kind, $6, $7, $8)
			ON CONFLICT (org_id, level, owner_id, kind, name) DO UPDATE SET description = EXCLUDED.description, tags = EXCLUDED.tags, updated_at = now()
			RETURNING id::text`, orgID, level, teamID, projectID, kind, name, description, tags).Scan(&bundleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO bundle_versions (bundle_id, version, files, checksum, published_by)
			SELECT $1, coalesce(max(version), 0) + 1, $2, $3, NULLIF($4, '')::uuid FROM bundle_versions WHERE bundle_id = $1`,
			bundleID, refsJSON, sum, publishedBy)
		return err
	})
	return bundleID, err
}

// PutTombstone 发布一条 tombstone 版本：在该落点上删除（或屏蔽上层的）同名资源。
func (d *DB) PutTombstone(ctx context.Context, orgID string, level resources.Level, ownerID string, kind resources.Kind, name, publishedBy string) error {
	teamID, projectID := "", ""
	switch level {
	case resources.LevelTeam:
		teamID = ownerID
	case resources.LevelProject:
		projectID = ownerID
	}
	return pg.WithTx(ctx, d.pool, func(tx pgx.Tx) error {
		var bundleID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO bundles (org_id, level, team_id, project_id, kind, name)
			VALUES ($1, $2::owner_level, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, $5::bundle_kind, $6)
			ON CONFLICT (org_id, level, owner_id, kind, name) DO UPDATE SET updated_at = now()
			RETURNING id::text`, orgID, level, teamID, projectID, kind, name).Scan(&bundleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO bundle_versions (bundle_id, version, files, checksum, deleted, published_by)
			SELECT $1, coalesce(max(version), 0) + 1, '[]'::jsonb, 'tombstone', true, NULLIF($2, '')::uuid FROM bundle_versions WHERE bundle_id = $1`,
			bundleID, publishedBy)
		return err
	})
}
