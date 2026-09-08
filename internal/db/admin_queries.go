package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ith5/ith5/internal/core"
)

var ErrConflict = errors.New("已存在")

// ---------------------------------------------------------------
// Bundle
// ---------------------------------------------------------------

type BundleRow struct {
	ID            string
	Name          string
	Kind          string
	Scope         string
	ProjectID     string
	Description   string
	Archived      bool
	LatestVersion int
	Checksum      string
	GroupNames    []string
	UpdatedAt     time.Time
}

func (d *DB) ListBundles(ctx context.Context, orgID string) ([]BundleRow, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT b.id::text, b.name, b.kind::text, b.scope::text,
		       coalesce(b.project_id::text,''), b.description, b.archived,
		       coalesce(v.version,0), coalesce(v.checksum,''), b.updated_at,
		       coalesce(array_agg(g.name) FILTER (WHERE g.name IS NOT NULL), '{}')
		FROM bundles b
		LEFT JOIN LATERAL (
		    SELECT version, checksum FROM bundle_versions
		    WHERE bundle_id = b.id ORDER BY version DESC LIMIT 1) v ON true
		LEFT JOIN permission_group_bundles pb ON pb.bundle_id = b.id
		LEFT JOIN permission_groups g ON g.id = pb.group_id AND NOT g.archived
		WHERE b.org_id = $1
		GROUP BY b.id, v.version, v.checksum
		ORDER BY b.archived, b.name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BundleRow
	for rows.Next() {
		var b BundleRow
		if err := rows.Scan(&b.ID, &b.Name, &b.Kind, &b.Scope, &b.ProjectID,
			&b.Description, &b.Archived, &b.LatestVersion, &b.Checksum,
			&b.UpdatedAt, &b.GroupNames); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// CreateBundle 新建 bundle，并**同时种好一份可直接编辑的 SKILL.md 草稿**。
//
// 若新建出来是空的，管理员必须自己知道「文件名必须叫 SKILL.md」，
// 打错就发布失败 —— 那是个陷阱。模板还保证 frontmatter 与 kind 一致。
// content 非空时用它作为正文，否则用模板占位。
func (d *DB) CreateBundle(ctx context.Context, orgID, name, kind, desc, content string) (string, error) {
	if err := core.ValidateName(name); err != nil {
		return "", err
	}
	if content == "" {
		content = core.SeedSkillMD(core.Kind(kind), name, desc)
	}
	// 入口文件名按形态定：目录形态是 SKILL.md，文件形态是 AGENT.md。
	// 种错名字的话，管理员会在发布时才收到「必须包含 XXX.md」，
	// 而那时他已经把正文写进了一个错名文件里。
	entry := core.Kind(kind).Shape().EntryFile()
	draft, err := json.Marshal([]core.File{{Path: entry, Content: content}})
	if err != nil {
		return "", err
	}
	var id string
	err = d.pool.QueryRow(ctx, `
		INSERT INTO bundles (org_id, name, kind, scope, description, draft_files)
		VALUES ($1,$2,$3::bundle_kind,'enterprise',$4,$5) RETURNING id::text`,
		orgID, name, kind, desc, draft).Scan(&id)
	if err != nil && isUniqueViolation(err) {
		return "", fmt.Errorf("%w: 同名 %s 已存在", ErrConflict, name)
	}
	return id, err
}

type BundleDetail struct {
	BundleRow
	DraftFiles []core.File
}

func (d *DB) GetBundle(ctx context.Context, orgID, id string) (BundleDetail, error) {
	var b BundleDetail
	var draft []core.File
	err := d.pool.QueryRow(ctx, `
		SELECT b.id::text, b.name, b.kind::text, b.scope::text, b.description,
		       b.archived, coalesce(v.version,0), coalesce(v.checksum,''),
		       b.updated_at, b.draft_files
		FROM bundles b
		LEFT JOIN LATERAL (
		    SELECT version, checksum FROM bundle_versions
		    WHERE bundle_id = b.id ORDER BY version DESC LIMIT 1) v ON true
		WHERE b.org_id = $1 AND b.id = $2`, orgID, id).
		Scan(&b.ID, &b.Name, &b.Kind, &b.Scope, &b.Description, &b.Archived,
			&b.LatestVersion, &b.Checksum, &b.UpdatedAt, &draft)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	b.DraftFiles = draft
	return b, err
}

func (d *DB) SaveDraft(ctx context.Context, orgID, id string, files []core.File) error {
	var kind string
	err := d.pool.QueryRow(ctx,
		`SELECT kind::text FROM bundles WHERE org_id=$1 AND id=$2`, orgID, id).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := core.ValidateFiles(core.Kind(kind), files); err != nil {
		return err
	}
	blob, err := json.Marshal(core.CanonicalFiles(files))
	if err != nil {
		return err
	}
	tag, err := d.pool.Exec(ctx, `
		UPDATE bundles SET draft_files = $3, updated_at = now()
		WHERE org_id = $1 AND id = $2`, orgID, id, blob)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Publish 把草稿冻结为新版本。
//
// 版本号在事务内串行生成（技术方案 §5.5），并由唯一约束兜底：
// 两名管理员同时发布时，其中一个会撞唯一键，重试即可拿到下一个号。
func (d *DB) Publish(ctx context.Context, orgID, id, userID, changelog string, rollbackOf int) (int, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// 锁住 bundle 行，避免并发发布产生同号
	var draft []core.File
	err = tx.QueryRow(ctx, `
		SELECT draft_files FROM bundles WHERE org_id=$1 AND id=$2 FOR UPDATE`,
		orgID, id).Scan(&draft)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	// kind 只是后台展示用的标签，Claude Code 只认 frontmatter。
	// 两者不一致时 kind 就成了谎话，必须在这里挡下来。
	var kind, bundleName string
	if err := tx.QueryRow(ctx,
		`SELECT kind::text, name FROM bundles WHERE id=$1`, id).Scan(&kind, &bundleName); err != nil {
		return 0, err
	}
	if err := core.ValidateForPublish(core.Kind(kind), bundleName, draft); err != nil {
		return 0, fmt.Errorf("草稿不可发布: %w", err)
	}
	sum, err := core.Checksum(draft)
	if err != nil {
		return 0, err
	}
	blob, err := json.Marshal(core.CanonicalFiles(draft))
	if err != nil {
		return 0, err
	}

	var next int
	if err := tx.QueryRow(ctx,
		`SELECT coalesce(max(version),0)+1 FROM bundle_versions WHERE bundle_id=$1`, id).
		Scan(&next); err != nil {
		return 0, err
	}
	var rb any
	if rollbackOf > 0 {
		rb = rollbackOf
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO bundle_versions (bundle_id,version,files,checksum,changelog,rollback_of_version,published_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, id, next, blob, sum, changelog, rb, userID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE bundles SET updated_at=now() WHERE id=$1`, id); err != nil {
		return 0, err
	}
	return next, tx.Commit(ctx)
}

type VersionRow struct {
	Version     int
	Checksum    string
	Changelog   string
	RollbackOf  int
	PublishedBy string
	PublishedAt time.Time
}

func (d *DB) ListVersions(ctx context.Context, orgID, id string) ([]VersionRow, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT v.version, v.checksum, v.changelog, coalesce(v.rollback_of_version,0),
		       coalesce(u.email,''), v.published_at
		FROM bundle_versions v
		JOIN bundles b ON b.id = v.bundle_id
		LEFT JOIN users u ON u.id = v.published_by
		WHERE b.org_id = $1 AND b.id = $2 ORDER BY v.version DESC`, orgID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VersionRow
	for rows.Next() {
		var v VersionRow
		if err := rows.Scan(&v.Version, &v.Checksum, &v.Changelog, &v.RollbackOf,
			&v.PublishedBy, &v.PublishedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// LoadVersionIntoDraft 把某个历史版本的内容写回草稿，供回滚使用。
// 回滚不是「把版本指针往回拨」，而是用旧内容发布一个新版本
// —— 客户端因此永远不需要处理降级语义（技术方案 §7.1）。
func (d *DB) LoadVersionIntoDraft(ctx context.Context, orgID, id string, version int) error {
	tag, err := d.pool.Exec(ctx, `
		UPDATE bundles SET draft_files = v.files, updated_at = now()
		FROM bundle_versions v
		WHERE bundles.id = $2 AND bundles.org_id = $1
		  AND v.bundle_id = bundles.id AND v.version = $3`, orgID, id, version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) SetBundleArchived(ctx context.Context, orgID, id string, archived bool) error {
	tag, err := d.pool.Exec(ctx,
		`UPDATE bundles SET archived=$3, updated_at=now() WHERE org_id=$1 AND id=$2`,
		orgID, id, archived)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func isUniqueViolation(err error) bool {
	return err != nil && (contains(err.Error(), "duplicate key") || contains(err.Error(), "23505"))
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
