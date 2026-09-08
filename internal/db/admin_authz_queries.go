package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------
// 权限组
// ---------------------------------------------------------------

type GroupRow struct {
	ID          string
	Key         string
	Name        string
	Description string
	Archived    bool
	BundleIDs   []string
	BundleNames []string
	// AssignedTo 是被授权的主体数，让管理员一眼看出「这个组给了几个人」
	AssignedTo int
}

func (d *DB) ListGroups(ctx context.Context, orgID string) ([]GroupRow, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT g.id::text, g.key, g.name, g.description, g.archived,
		       coalesce(array_agg(DISTINCT pb.bundle_id::text) FILTER (WHERE pb.bundle_id IS NOT NULL),'{}'),
		       coalesce(array_agg(DISTINCT b.name) FILTER (WHERE b.name IS NOT NULL),'{}'),
		       (SELECT count(*) FROM assignments a WHERE a.group_id = g.id)
		FROM permission_groups g
		LEFT JOIN permission_group_bundles pb ON pb.group_id = g.id
		LEFT JOIN bundles b ON b.id = pb.bundle_id
		WHERE g.org_id = $1
		GROUP BY g.id ORDER BY g.archived, g.name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupRow
	for rows.Next() {
		var g GroupRow
		if err := rows.Scan(&g.ID, &g.Key, &g.Name, &g.Description, &g.Archived,
			&g.BundleIDs, &g.BundleNames, &g.AssignedTo); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (d *DB) CreateGroup(ctx context.Context, orgID, key, name, desc string) (string, error) {
	var id string
	err := d.pool.QueryRow(ctx, `
		INSERT INTO permission_groups (org_id, key, name, description)
		VALUES ($1,$2,$3,$4) RETURNING id::text`, orgID, key, name, desc).Scan(&id)
	if isUniqueViolation(err) {
		return "", ErrConflict
	}
	return id, err
}

// SetGroupBundles 全量替换权限组的成员。
//
// 往组里加一个 bundle 后，**所有被授权者下次 sync 即自动拿到**，
// 不需要改任何一条授权 —— 这是权限组存在的主要理由。
func (d *DB) SetGroupBundles(ctx context.Context, orgID, groupID string, bundleIDs []string) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var n int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM permission_groups WHERE org_id=$1 AND id=$2`, orgID, groupID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM permission_group_bundles WHERE group_id=$1`, groupID); err != nil {
		return err
	}
	for _, bid := range bundleIDs {
		// 只接受同 org 的 bundle，防止跨租户装配
		if _, err := tx.Exec(ctx, `
			INSERT INTO permission_group_bundles (group_id, bundle_id)
			SELECT $1, b.id FROM bundles b WHERE b.id = $2 AND b.org_id = $3`,
			groupID, bid, orgID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE permission_groups SET updated_at=now() WHERE id=$1`, groupID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (d *DB) SetGroupArchived(ctx context.Context, orgID, id string, archived bool) error {
	tag, err := d.pool.Exec(ctx,
		`UPDATE permission_groups SET archived=$3, updated_at=now() WHERE org_id=$1 AND id=$2`,
		orgID, id, archived)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------
// 授权
// ---------------------------------------------------------------

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

func (d *DB) ListAssignments(ctx context.Context, orgID string) ([]AssignmentRow, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT a.id::text, coalesce(a.bundle_id::text,''), coalesce(b.name,''),
		       coalesce(a.group_id::text,''), coalesce(g.name,''),
		       a.subject_type::text, coalesce(a.subject_id,''),
		       coalesce(u.email, p.name, ''), a.expires_at, a.created_at
		FROM assignments a
		LEFT JOIN bundles b ON b.id = a.bundle_id
		LEFT JOIN permission_groups g ON g.id = a.group_id
		LEFT JOIN users u ON a.subject_type='user' AND u.id::text = a.subject_id
		LEFT JOIN projects p ON a.subject_type='project' AND p.id::text = a.subject_id
		WHERE a.org_id = $1 ORDER BY a.created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AssignmentRow
	for rows.Next() {
		var a AssignmentRow
		if err := rows.Scan(&a.ID, &a.BundleID, &a.BundleName, &a.GroupID, &a.GroupName,
			&a.SubjectType, &a.SubjectID, &a.SubjectName, &a.ExpiresAt, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (d *DB) CreateAssignment(ctx context.Context, orgID, bundleID, groupID, subjectType, subjectID string, expires *time.Time) (string, error) {
	var bid, gid, sid any
	if bundleID != "" {
		bid = bundleID
	}
	if groupID != "" {
		gid = groupID
	}
	if subjectID != "" {
		sid = subjectID
	}
	var id string
	err := d.pool.QueryRow(ctx, `
		INSERT INTO assignments (org_id, bundle_id, group_id, subject_type, subject_id, expires_at)
		VALUES ($1,$2,$3,$4::assignment_subject,$5,$6) RETURNING id::text`,
		orgID, bid, gid, subjectType, sid, expires).Scan(&id)
	if isUniqueViolation(err) {
		// 续期是 UPDATE 不是新增行（技术方案 §5.5）
		err = d.pool.QueryRow(ctx, `
			UPDATE assignments SET expires_at = $6
			WHERE org_id=$1 AND coalesce(bundle_id::text,'')=coalesce($2::text,'')
			  AND coalesce(group_id::text,'')=coalesce($3::text,'')
			  AND subject_type=$4::assignment_subject
			  AND coalesce(subject_id,'')=coalesce($5,'')
			RETURNING id::text`, orgID, bid, gid, subjectType, sid, expires).Scan(&id)
	}
	return id, err
}

func (d *DB) DeleteAssignment(ctx context.Context, orgID, id string) error {
	tag, err := d.pool.Exec(ctx, `DELETE FROM assignments WHERE org_id=$1 AND id=$2`, orgID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------
// 成员、审计与健康
// ---------------------------------------------------------------

type MemberRow struct {
	ID         string
	Email      string
	Name       string
	Role       string
	Status     string
	Machines   int
	LastSeenAt *time.Time
}

func (d *DB) ListMembers(ctx context.Context, orgID string) ([]MemberRow, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT u.id::text, u.email, u.name, u.role::text, u.status::text,
		       count(m.id), max(m.last_seen_at)
		FROM users u LEFT JOIN machines m ON m.user_id = u.id
		WHERE u.org_id = $1 GROUP BY u.id ORDER BY u.email`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemberRow
	for rows.Next() {
		var m MemberRow
		if err := rows.Scan(&m.ID, &m.Email, &m.Name, &m.Role, &m.Status, &m.Machines, &m.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (d *DB) SetMemberStatus(ctx context.Context, orgID, id, status string) error {
	tag, err := d.pool.Exec(ctx,
		`UPDATE users SET status=$3::user_status WHERE org_id=$1 AND id=$2`, orgID, id, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type AuditRow struct {
	Email      string
	Hostname   string
	BundleName string
	Version    int
	Action     string
	Detail     string
	CreatedAt  time.Time
}

// ListDistributions 支持按 action 过滤；传 "conflict_skipped" 即「下发受阻」列表。
func (d *DB) ListDistributions(ctx context.Context, orgID, action string, limit int) ([]AuditRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := d.pool.Query(ctx, `
		SELECT u.email, coalesce(m.hostname,''), b.name, d.version, d.action::text,
		       coalesce(d.detail::text,''), d.created_at
		FROM distribution_logs d
		JOIN users u ON u.id = d.user_id
		JOIN bundles b ON b.id = d.bundle_id
		LEFT JOIN machines m ON m.id = d.machine_id
		WHERE d.org_id = $1 AND ($2 = '' OR d.action::text = $2)
		ORDER BY d.created_at DESC LIMIT $3`, orgID, action, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditRow
	for rows.Next() {
		var a AuditRow
		if err := rows.Scan(&a.Email, &a.Hostname, &a.BundleName, &a.Version,
			&a.Action, &a.Detail, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type StaleRow struct {
	Email      string
	Hostname   string
	LastSeenAt time.Time
	Days       int
	// UserLevel 为 true 表示该用户**所有**设备都掉队 —— 可能已离职未处理。
	// 部分设备掉队通常只是换机或闲置，优先级低（技术方案 §17.3）。
	UserLevel bool
}

func (d *DB) ListStaleMachines(ctx context.Context, orgID string) ([]StaleRow, error) {
	rows, err := d.pool.Query(ctx, `
		WITH th AS (SELECT stale_threshold_days AS d FROM organizations WHERE id = $1),
		stale AS (
		  SELECT u.id AS uid, u.email, m.hostname, m.last_seen_at,
		         extract(day FROM now() - m.last_seen_at)::int AS days,
		         m.last_seen_at < now() - ((SELECT d FROM th) || ' days')::interval AS is_stale
		  FROM machines m JOIN users u ON u.id = m.user_id
		  WHERE u.org_id = $1
		)
		SELECT email, hostname, last_seen_at, days,
		       bool_and(is_stale) OVER (PARTITION BY uid) AS user_level
		FROM stale WHERE is_stale ORDER BY last_seen_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StaleRow
	for rows.Next() {
		var s StaleRow
		if err := rows.Scan(&s.Email, &s.Hostname, &s.LastSeenAt, &s.Days, &s.UserLevel); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) GetUserProjects(ctx context.Context, userID string) ([]string, error) {
	return d.ProjectIDs(ctx, userID)
}

var _ = errors.Is
var _ = pgx.ErrNoRows

// CreateMember 在组织内建号。password_hash 由调用方算好传进来，
// 这一层不认识明文密码。
func (d *DB) CreateMember(ctx context.Context, orgID, email, name, role, pwHash string) (string, error) {
	var id string
	err := d.pool.QueryRow(ctx, `
		INSERT INTO users (org_id, email, name, role, status, password_hash)
		VALUES ($1,$2,$3,$4::user_role,'active',$5) RETURNING id::text`,
		orgID, email, name, role, pwHash).Scan(&id)
	if isUniqueViolation(err) {
		return "", ErrConflict
	}
	return id, err
}

// SetMemberPassword 重置成员密码。
//
// 刻意不动 status，也不吊销已签发的令牌：改密码是「以后用新密码登录」，
// 不是「踢下线」——后者是停用要干的事。
func (d *DB) SetMemberPassword(ctx context.Context, orgID, id, pwHash string) error {
	tag, err := d.pool.Exec(ctx,
		`UPDATE users SET password_hash=$3 WHERE org_id=$1 AND id=$2`, orgID, id, pwHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MemberRole 读一个成员的角色，供越权校验用（admin 不能碰 owner/admin）。
func (d *DB) MemberRole(ctx context.Context, orgID, id string) (string, error) {
	var role string
	err := d.pool.QueryRow(ctx,
		`SELECT role::text FROM users WHERE org_id=$1 AND id=$2`, orgID, id).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return role, err
}
