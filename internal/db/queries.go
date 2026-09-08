package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ith5/ith5/internal/core"
)

var (
	ErrNotFound  = errors.New("未找到")
	ErrSuspended = errors.New("账号已停用")
)

// ---------------------------------------------------------------
// 用户与设备
// ---------------------------------------------------------------

type User struct {
	ID        string
	OrgID     string
	Email     string
	Role      string
	Suspended bool
}

// GetUser 读取用户实时状态。
//
// 每次业务请求都要调用：尚未过期的访问令牌不能让一个已停用的账号继续访问
// （D4，技术方案 §6.2）。
func (d *DB) GetUser(ctx context.Context, id string) (User, error) {
	var u User
	var status string
	err := d.pool.QueryRow(ctx,
		`SELECT id::text, org_id::text, email, role::text, status::text FROM users WHERE id = $1`,
		id).Scan(&u.ID, &u.OrgID, &u.Email, &u.Role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	if err != nil {
		return u, err
	}
	u.Suspended = status == "suspended"
	return u, nil
}

func (d *DB) GetUserByEmail(ctx context.Context, orgSlug, email string) (User, string, error) {
	var u User
	var status, pwHash string
	err := d.pool.QueryRow(ctx, `
		SELECT u.id::text, u.org_id::text, u.email, u.role::text, u.status::text,
		       coalesce(u.password_hash,'')
		FROM users u JOIN organizations o ON o.id = u.org_id
		WHERE o.slug = $1 AND u.email = $2`, orgSlug, email).
		Scan(&u.ID, &u.OrgID, &u.Email, &u.Role, &status, &pwHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, "", ErrNotFound
	}
	if err != nil {
		return u, "", err
	}
	u.Suspended = status == "suspended"
	return u, pwHash, nil
}

// UpsertMachine 按 (user_id, fingerprint) 幂等登记设备。
func (d *DB) UpsertMachine(ctx context.Context, userID, fingerprint, hostname, os string) (string, error) {
	var id string
	err := d.pool.QueryRow(ctx, `
		INSERT INTO machines (user_id, fingerprint, hostname, os)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (user_id, fingerprint)
		DO UPDATE SET hostname = EXCLUDED.hostname, os = EXCLUDED.os, last_seen_at = now()
		RETURNING id::text`, userID, fingerprint, hostname, os).Scan(&id)
	return id, err
}

// TouchMachine 更新 last_seen_at，供「长期无 sync」扫描使用（技术方案 §17.3）。
func (d *DB) TouchMachine(ctx context.Context, machineID string) error {
	_, err := d.pool.Exec(ctx, `UPDATE machines SET last_seen_at = now() WHERE id = $1`, machineID)
	return err
}

func (d *DB) ProjectIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT project_id::text FROM project_members WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------
// 授权数据
// ---------------------------------------------------------------

// AuthzData 是一个组织的全部授权相关数据。
//
// 一次性读出来交给 core.Resolve 判定，保持 core 为纯函数。
// V1 的规模（数百人、数十个 bundle）下这个查询代价可忽略；
// 若将来数据量上来，可改为按 principal 收窄查询范围，core 的接口不变。
type AuthzData struct {
	Bundles     []core.BundleMeta
	Groups      []core.PermissionGroup
	Assignments []core.Assignment
}

func (d *DB) LoadAuthzData(ctx context.Context, orgID string) (AuthzData, error) {
	var a AuthzData

	// Bundle 及其最新已发布版本
	rows, err := d.pool.Query(ctx, `
		SELECT b.id::text, b.org_id::text, b.name, b.kind::text, b.scope::text,
		       coalesce(b.project_id::text, ''), b.archived, b.description,
		       coalesce(v.version, 0), coalesce(v.checksum, '')
		FROM bundles b
		LEFT JOIN LATERAL (
		    SELECT version, checksum FROM bundle_versions
		    WHERE bundle_id = b.id ORDER BY version DESC LIMIT 1
		) v ON true
		WHERE b.org_id = $1`, orgID)
	if err != nil {
		return a, fmt.Errorf("读取 bundles: %w", err)
	}
	for rows.Next() {
		var b core.BundleMeta
		var kind, scope string
		if err := rows.Scan(&b.ID, &b.OrgID, &b.Name, &kind, &scope,
			&b.ProjectID, &b.Archived, &b.Description, &b.Version, &b.Checksum); err != nil {
			rows.Close()
			return a, err
		}
		b.Kind, b.Scope = core.Kind(kind), core.Scope(scope)
		a.Bundles = append(a.Bundles, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return a, err
	}

	// 权限组及其成员 bundle
	rows, err = d.pool.Query(ctx, `
		SELECT g.id::text, g.org_id::text, g.key, g.name, g.archived,
		       coalesce(array_agg(pb.bundle_id::text) FILTER (WHERE pb.bundle_id IS NOT NULL), '{}')
		FROM permission_groups g
		LEFT JOIN permission_group_bundles pb ON pb.group_id = g.id
		WHERE g.org_id = $1
		GROUP BY g.id`, orgID)
	if err != nil {
		return a, fmt.Errorf("读取权限组: %w", err)
	}
	for rows.Next() {
		var g core.PermissionGroup
		if err := rows.Scan(&g.ID, &g.OrgID, &g.Key, &g.Name, &g.Archived, &g.BundleIDs); err != nil {
			rows.Close()
			return a, err
		}
		a.Groups = append(a.Groups, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return a, err
	}

	// 授权
	rows, err = d.pool.Query(ctx, `
		SELECT org_id::text, coalesce(bundle_id::text,''), coalesce(group_id::text,''),
		       subject_type::text, coalesce(subject_id,''), expires_at
		FROM assignments WHERE org_id = $1`, orgID)
	if err != nil {
		return a, fmt.Errorf("读取授权: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var as core.Assignment
		var st string
		var exp *time.Time
		if err := rows.Scan(&as.OrgID, &as.BundleID, &as.GroupID, &st, &as.SubjectID, &exp); err != nil {
			return a, err
		}
		as.SubjectType, as.ExpiresAt = core.SubjectType(st), exp
		a.Assignments = append(a.Assignments, as)
	}
	return a, rows.Err()
}

// GetBundleVersion 读取正文。org 与 bundle 同时约束，避免跨租户读取。
func (d *DB) GetBundleVersion(ctx context.Context, orgID, bundleID string, version int) (core.BundleMeta, []core.File, error) {
	var m core.BundleMeta
	var kind string
	var files []core.File
	err := d.pool.QueryRow(ctx, `
		SELECT b.id::text, b.org_id::text, b.name, b.kind::text, v.version, v.checksum, v.files
		FROM bundle_versions v JOIN bundles b ON b.id = v.bundle_id
		WHERE b.org_id = $1 AND b.id = $2 AND v.version = $3`,
		orgID, bundleID, version).
		Scan(&m.ID, &m.OrgID, &m.Name, &kind, &m.Version, &m.Checksum, &files)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, nil, ErrNotFound
	}
	if err != nil {
		return m, nil, err
	}
	m.Kind = core.Kind(kind)
	return m, files, nil
}
