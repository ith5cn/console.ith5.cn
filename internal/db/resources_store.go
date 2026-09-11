package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ith5/ith5/internal/resources"
)

// ResourcesStore 是后台读资源与上传 blob 的仓储（docs/开发规格.md §1.5、§1.6 的 POST /v1/blobs）。
type ResourcesStore struct{ db *DB }

// Resources 返回资源读仓储。
func (d *DB) Resources() *ResourcesStore { return &ResourcesStore{db: d} }

// ResourceRow 是后台列表的一行：资源元数据加当前版本摘要。
type ResourceRow struct {
	resources.Resource
	Namespace   string
	VersionID   string
	Files       []resources.FileRef
	PublishedAt *time.Time
	UpdatedAt   time.Time
	Groups      []string
}

// VersionRow 是版本历史的一行。
type VersionRow struct {
	ID                string
	Version           int
	Files             []resources.FileRef
	Checksum          string
	Deleted           bool
	Changelog         string
	RollbackOfVersion *int
	ReleaseID         string
	PublishedBy       string
	PublishedAt       time.Time
}

// ErrBlobTooLarge 表示上传超过上限。
var ErrBlobTooLarge = errors.New("db: blob 超过大小上限")

// ErrBlobHashMismatch 表示声明的哈希与内容不符。
var ErrBlobHashMismatch = errors.New("db: blob 哈希与内容不符")

// PutBlob 写入一段内容。sha 必须等于内容的 BlobSum；重复写入幂等。
func (s *ResourcesStore) PutBlob(ctx context.Context, orgID, sha string, content []byte, maxBytes int) error {
	if len(content) > maxBytes {
		return ErrBlobTooLarge
	}
	if resources.BlobSum(content) != sha {
		return ErrBlobHashMismatch
	}
	_, err := s.db.pool.Exec(ctx, `
		INSERT INTO blobs (org_id, sha256, bytes, size) VALUES ($1, $2, $3, $4)
		ON CONFLICT (org_id, sha256) DO NOTHING`, orgID, sha, content, len(content))
	return err
}

// HasBlob 判断 blob 是否已存在，供上传前查询。
func (s *ResourcesStore) HasBlob(ctx context.Context, orgID, sha string) (bool, error) {
	var ok bool
	err := s.db.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM blobs WHERE org_id = $1 AND sha256 = $2)`, orgID, sha).Scan(&ok)
	return ok, err
}

// List 按层级、落点、类型、关键字筛选，含 tombstone（后台要能看到已删除的）。
func (s *ResourcesStore) List(ctx context.Context, orgID string, level resources.Level, ownerID string, kind resources.Kind, q string, limit int) ([]ResourceRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.pool.Query(ctx, `
		WITH cur AS (
			SELECT DISTINCT ON (bundle_id) bundle_id, id, version, files, checksum, deleted, published_at
			FROM bundle_versions ORDER BY bundle_id, version DESC
		)
		SELECT b.id::text, b.org_id::text, b.level::text, b.owner_id::text, b.kind::text, b.name, b.description,
		       b.tags, b.archived, cur.id::text, cur.version, cur.files, cur.checksum, cur.deleted, cur.published_at, b.updated_at,
		       coalesce(p.slug, ''), coalesce(t.slug, ''),
		       coalesce((SELECT array_agg(g.key ORDER BY g.key) FROM permission_group_bundles gb JOIN permission_groups g ON g.id = gb.group_id WHERE gb.bundle_id = b.id), '{}')
		FROM bundles b
		JOIN cur ON cur.bundle_id = b.id
		LEFT JOIN projects p ON p.id = b.project_id
		LEFT JOIN teams t ON t.id = b.team_id
		WHERE b.org_id = $1
		  AND ($2 = '' OR b.level::text = $2)
		  AND ($3 = '' OR b.owner_id::text = $3)
		  AND ($4 = '' OR b.kind::text = $4)
		  AND ($5 = '' OR b.name ILIKE '%' || $5 || '%' OR b.description ILIKE '%' || $5 || '%')
		ORDER BY b.level, b.kind, b.name LIMIT $6`, orgID, string(level), ownerID, string(kind), q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ResourceRow{}
	for rows.Next() {
		r, err := scanResourceRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanResourceRow(row pgx.Row) (ResourceRow, error) {
	var r ResourceRow
	var level, kind, projectSlug, teamSlug string
	var files []byte
	err := row.Scan(&r.ID, &r.OrgID, &level, &r.OwnerID, &kind, &r.Name, &r.Description, &r.Tags, &r.Archived,
		&r.VersionID, &r.Version, &files, &r.Checksum, &r.Deleted, &r.PublishedAt, &r.UpdatedAt, &projectSlug, &teamSlug, &r.Groups)
	if err != nil {
		return r, err
	}
	r.Level, r.Kind = resources.Level(level), resources.Kind(kind)
	_ = json.Unmarshal(files, &r.Files)
	switch r.Level {
	case resources.LevelProject:
		r.Namespace = projectSlug
	case resources.LevelTeam:
		r.Namespace = teamSlug
	default:
		r.Namespace = "common"
	}
	return r, nil
}

// Get 读一条资源及其版本历史。
func (s *ResourcesStore) Get(ctx context.Context, orgID, id string) (ResourceRow, []VersionRow, error) {
	list, err := s.listByID(ctx, orgID, id)
	if err != nil {
		return ResourceRow{}, nil, err
	}
	if len(list) == 0 {
		return ResourceRow{}, nil, resources.ErrNotFound
	}
	rows, err := s.db.pool.Query(ctx, `
		SELECT v.id::text, v.version, v.files, v.checksum, v.deleted, v.changelog, v.rollback_of_version,
		       coalesce(v.release_id::text, ''), coalesce(u.email, ''), v.published_at
		FROM bundle_versions v LEFT JOIN users u ON u.id = v.published_by
		WHERE v.bundle_id = $1 ORDER BY v.version DESC`, id)
	if err != nil {
		return ResourceRow{}, nil, err
	}
	defer rows.Close()
	versions := []VersionRow{}
	for rows.Next() {
		var v VersionRow
		var files []byte
		if err := rows.Scan(&v.ID, &v.Version, &files, &v.Checksum, &v.Deleted, &v.Changelog, &v.RollbackOfVersion,
			&v.ReleaseID, &v.PublishedBy, &v.PublishedAt); err != nil {
			return ResourceRow{}, nil, err
		}
		_ = json.Unmarshal(files, &v.Files)
		versions = append(versions, v)
	}
	return list[0], versions, rows.Err()
}

func (s *ResourcesStore) listByID(ctx context.Context, orgID, id string) ([]ResourceRow, error) {
	rows, err := s.db.pool.Query(ctx, `
		WITH cur AS (
			SELECT DISTINCT ON (bundle_id) bundle_id, id, version, files, checksum, deleted, published_at
			FROM bundle_versions ORDER BY bundle_id, version DESC
		)
		SELECT b.id::text, b.org_id::text, b.level::text, b.owner_id::text, b.kind::text, b.name, b.description,
		       b.tags, b.archived, cur.id::text, cur.version, cur.files, cur.checksum, cur.deleted, cur.published_at, b.updated_at,
		       coalesce(p.slug, ''), coalesce(t.slug, ''),
		       coalesce((SELECT array_agg(g.key ORDER BY g.key) FROM permission_group_bundles gb JOIN permission_groups g ON g.id = gb.group_id WHERE gb.bundle_id = b.id), '{}')
		FROM bundles b
		JOIN cur ON cur.bundle_id = b.id
		LEFT JOIN projects p ON p.id = b.project_id
		LEFT JOIN teams t ON t.id = b.team_id
		WHERE b.org_id = $1 AND b.id = $2`, orgID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ResourceRow
	for rows.Next() {
		r, err := scanResourceRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetVersion 读某个版本的文件清单，并返回 bundle 元数据用于权限判断。
func (s *ResourcesStore) GetVersion(ctx context.Context, orgID, versionID string) (ResourceRow, VersionRow, error) {
	var bundleID string
	var v VersionRow
	var files []byte
	err := s.db.pool.QueryRow(ctx, `
		SELECT v.bundle_id::text, v.id::text, v.version, v.files, v.checksum, v.deleted, v.changelog, v.rollback_of_version,
		       coalesce(v.release_id::text, ''), coalesce(u.email, ''), v.published_at
		FROM bundle_versions v JOIN bundles b ON b.id = v.bundle_id LEFT JOIN users u ON u.id = v.published_by
		WHERE b.org_id = $1 AND v.id = $2`, orgID, versionID).
		Scan(&bundleID, &v.ID, &v.Version, &files, &v.Checksum, &v.Deleted, &v.Changelog, &v.RollbackOfVersion, &v.ReleaseID, &v.PublishedBy, &v.PublishedAt)
	if isNoRows(err) {
		return ResourceRow{}, v, resources.ErrNotFound
	}
	if err != nil {
		return ResourceRow{}, v, err
	}
	_ = json.Unmarshal(files, &v.Files)
	list, err := s.listByID(ctx, orgID, bundleID)
	if err != nil || len(list) == 0 {
		return ResourceRow{}, v, resources.ErrNotFound
	}
	return list[0], v, nil
}

// SetTags 覆盖资源标签。
func (s *ResourcesStore) SetTags(ctx context.Context, orgID, id string, tags []string) error {
	if tags == nil {
		tags = []string{}
	}
	tag, err := s.db.pool.Exec(ctx, `UPDATE bundles SET tags = $3, updated_at = now() WHERE org_id = $1 AND id = $2`, orgID, id, tags)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return resources.ErrNotFound
	}
	return nil
}

// ReadBlob 无可见性判断地读内容，只给已经通过资源级权限检查的后台接口用。
func (s *ResourcesStore) ReadBlob(ctx context.Context, orgID, sha string) ([]byte, error) {
	var b []byte
	err := s.db.pool.QueryRow(ctx, `SELECT bytes FROM blobs WHERE org_id = $1 AND sha256 = $2`, orgID, sha).Scan(&b)
	if isNoRows(err) {
		return nil, resources.ErrNotFound
	}
	return b, err
}
