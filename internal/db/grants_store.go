package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ith5/ith5/internal/platform/pg"
)

// GrantsStore 管权限组与授权（docs/设计-资源类型与层级.md §5）。
//
// 这是 ITH5 在 teamai 模型之外保留的差异化能力：有名字的可选内容包，授权给用户 / 项目 / 组织，可到期。
type GrantsStore struct{ db *DB }

// Grants 返回权限组与授权仓储。
func (d *DB) Grants() *GrantsStore { return &GrantsStore{db: d} }

// Group 是权限组的后台视图。
type Group struct {
	ID          string
	Key         string
	Name        string
	Description string
	Archived    bool
	BundleIDs   []string
	BundleNames []string
	AssignedTo  int
	CreatedAt   time.Time
}

// AssignmentRow 是一条授权的后台视图。
type AssignmentRow struct {
	ID          string
	BundleID    string
	BundleName  string
	GroupID     string
	GroupName   string
	SubjectType string
	SubjectID   string
	SubjectName string
	ExpiresAt   *time.Time
	CreatedAt   time.Time
}

var (
	ErrGroupKeyTaken     = errors.New("db: 权限组 key 已存在")
	ErrAssignmentExists  = errors.New("db: 相同授权已存在")
	ErrAssignmentTarget  = errors.New("db: 授权目标不存在")
	ErrAssignmentSubject = errors.New("db: 授权主体不存在")
)

func (s *GrantsStore) ListGroups(ctx context.Context, orgID string) ([]Group, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT g.id::text, g.key, g.name, g.description, g.archived, g.created_at,
		       coalesce(array_agg(b.id::text ORDER BY b.name) FILTER (WHERE b.id IS NOT NULL), '{}'),
		       coalesce(array_agg(b.kind::text || '/' || b.name ORDER BY b.name) FILTER (WHERE b.id IS NOT NULL), '{}'),
		       (SELECT count(*) FROM assignments a WHERE a.group_id = g.id)
		FROM permission_groups g
		LEFT JOIN permission_group_bundles gb ON gb.group_id = g.id
		LEFT JOIN bundles b ON b.id = gb.bundle_id
		WHERE g.org_id = $1 GROUP BY g.id ORDER BY g.archived, g.name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Key, &g.Name, &g.Description, &g.Archived, &g.CreatedAt, &g.BundleIDs, &g.BundleNames, &g.AssignedTo); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *GrantsStore) CreateGroup(ctx context.Context, orgID, key, name, description string) (string, error) {
	var id string
	err := s.db.pool.QueryRow(ctx, `
		INSERT INTO permission_groups (org_id, key, name, description) VALUES ($1, $2, $3, $4) RETURNING id::text`,
		orgID, key, name, description).Scan(&id)
	if isUniqueViolation(err) {
		return "", ErrGroupKeyTaken
	}
	return id, err
}

func (s *GrantsStore) UpdateGroup(ctx context.Context, orgID, id, name, description string, archived bool) error {
	tag, err := s.db.pool.Exec(ctx, `
		UPDATE permission_groups SET name = $3, description = $4, archived = $5, updated_at = now()
		WHERE org_id = $1 AND id = $2`, orgID, id, name, description, archived)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetGroupBundles 覆盖组内资源；只接受本组织的资源 id，其余静默忽略。
func (s *GrantsStore) SetGroupBundles(ctx context.Context, orgID, groupID string, bundleIDs []string) (int, error) {
	n := 0
	err := pg.WithTx(ctx, s.db.pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM permission_groups WHERE org_id = $1 AND id = $2)`, orgID, groupID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM permission_group_bundles WHERE group_id = $1`, groupID); err != nil {
			return err
		}
		for _, bid := range bundleIDs {
			tag, err := tx.Exec(ctx, `
				INSERT INTO permission_group_bundles (group_id, bundle_id)
				SELECT $1, b.id FROM bundles b WHERE b.org_id = $2 AND b.id::text = $3
				ON CONFLICT DO NOTHING`, groupID, orgID, bid)
			if err != nil {
				return err
			}
			n += int(tag.RowsAffected())
		}
		return nil
	})
	return n, err
}

func (s *GrantsStore) ListAssignments(ctx context.Context, orgID string) ([]AssignmentRow, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT a.id::text, coalesce(a.bundle_id::text, ''), coalesce(b.kind::text || '/' || b.name, ''),
		       coalesce(a.group_id::text, ''), coalesce(g.name, ''),
		       a.subject_type::text, coalesce(a.subject_id::text, ''),
		       CASE a.subject_type WHEN 'user' THEN coalesce(u.email, '') WHEN 'project' THEN coalesce(p.slug, '') ELSE '' END,
		       a.expires_at, a.created_at
		FROM assignments a
		LEFT JOIN bundles b ON b.id = a.bundle_id
		LEFT JOIN permission_groups g ON g.id = a.group_id
		LEFT JOIN users u ON a.subject_type = 'user' AND u.id = a.subject_id
		LEFT JOIN projects p ON a.subject_type = 'project' AND p.id = a.subject_id
		WHERE a.org_id = $1 ORDER BY a.created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssignmentRow{}
	for rows.Next() {
		var r AssignmentRow
		if err := rows.Scan(&r.ID, &r.BundleID, &r.BundleName, &r.GroupID, &r.GroupName, &r.SubjectType, &r.SubjectID, &r.SubjectName, &r.ExpiresAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateAssignment 建一条授权；目标与主体都必须属于本组织。
func (s *GrantsStore) CreateAssignment(ctx context.Context, orgID, bundleID, groupID, subjectType, subjectID string, expiresAt *time.Time) (string, error) {
	var ok bool
	switch {
	case bundleID != "":
		if err := s.db.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM bundles WHERE org_id = $1 AND id::text = $2)`, orgID, bundleID).Scan(&ok); err != nil {
			return "", err
		}
	case groupID != "":
		if err := s.db.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM permission_groups WHERE org_id = $1 AND id::text = $2)`, orgID, groupID).Scan(&ok); err != nil {
			return "", err
		}
	}
	if !ok {
		return "", ErrAssignmentTarget
	}
	switch subjectType {
	case "user":
		if err := s.db.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE org_id = $1 AND id::text = $2)`, orgID, subjectID).Scan(&ok); err != nil {
			return "", err
		}
	case "project":
		if err := s.db.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE org_id = $1 AND id::text = $2)`, orgID, subjectID).Scan(&ok); err != nil {
			return "", err
		}
	case "org":
		ok = true
	}
	if !ok {
		return "", ErrAssignmentSubject
	}
	var id string
	err := s.db.pool.QueryRow(ctx, `
		INSERT INTO assignments (org_id, bundle_id, group_id, subject_type, subject_id, expires_at)
		VALUES ($1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4::assignment_subject, NULLIF($5, '')::uuid, $6)
		RETURNING id::text`, orgID, bundleID, groupID, subjectType, subjectID, expiresAt).Scan(&id)
	if isUniqueViolation(err) {
		return "", ErrAssignmentExists
	}
	return id, err
}

func (s *GrantsStore) DeleteAssignment(ctx context.Context, orgID, id string) error {
	tag, err := s.db.pool.Exec(ctx, `DELETE FROM assignments WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------
// 分发回执与工具调用审计的后台读
// ---------------------------------------------------------------

// DistributionRow 是一条同步回执。
type DistributionRow struct {
	Email      string
	Hostname   string
	Kind       string
	Name       string
	Version    *int
	Action     string
	Detail     []byte
	Revision   string
	OccurredAt time.Time
}

func (s *GrantsStore) ListDistributions(ctx context.Context, orgID, action string, limit int) ([]DistributionRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.pool.Query(ctx, `
		SELECT u.email, coalesce(m.hostname, ''), d.kind::text, d.name, d.version, d.action::text, coalesce(d.detail, '{}'), coalesce(d.revision, ''), d.occurred_at
		FROM distribution_logs d JOIN users u ON u.id = d.user_id LEFT JOIN machines m ON m.id = d.machine_id
		WHERE d.org_id = $1 AND ($2 = '' OR d.action::text = $2)
		ORDER BY d.created_at DESC LIMIT $3`, orgID, action, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DistributionRow{}
	for rows.Next() {
		var r DistributionRow
		if err := rows.Scan(&r.Email, &r.Hostname, &r.Kind, &r.Name, &r.Version, &r.Action, &r.Detail, &r.Revision, &r.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ExecutionRow 是一条工具调用审计。
type ExecutionRow struct {
	Email      string
	Hostname   string
	SessionID  string
	EventType  string
	ToolName   string
	Summary    []byte
	OccurredAt time.Time
}

func (s *GrantsStore) ListExecutions(ctx context.Context, orgID, userID, tool string, limit int) ([]ExecutionRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.pool.Query(ctx, `
		SELECT u.email, coalesce(m.hostname, ''), e.session_id, e.event_type::text, coalesce(e.tool_name, ''), e.summary, e.occurred_at
		FROM execution_events e JOIN users u ON u.id = e.user_id LEFT JOIN machines m ON m.id = e.machine_id
		WHERE e.org_id = $1 AND ($2 = '' OR e.user_id::text = $2) AND ($3 = '' OR e.tool_name = $3)
		ORDER BY e.occurred_at DESC LIMIT $4`, orgID, userID, tool, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExecutionRow{}
	for rows.Next() {
		var r ExecutionRow
		if err := rows.Scan(&r.Email, &r.Hostname, &r.SessionID, &r.EventType, &r.ToolName, &r.Summary, &r.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
