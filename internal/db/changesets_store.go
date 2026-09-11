package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ith5/ith5/internal/changesets"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/platform/pg"
	"github.com/ith5/ith5/internal/releases"
	"github.com/ith5/ith5/internal/resources"
)

// ChangesetStore 实现 changesets.Store。
type ChangesetStore struct{ db *DB }

// Changesets 返回变更集仓储。
func (d *DB) Changesets() *ChangesetStore { return &ChangesetStore{db: d} }

// ReleaseStore 实现 releases.Store。发布事务同时碰变更集与内容表，但从调用方看它属于 releases。
type ReleaseStore struct{ db *DB }

// Releases 返回发布仓储。
func (d *DB) Releases() *ReleaseStore { return &ReleaseStore{db: d} }

var (
	_ changesets.Store = (*ChangesetStore)(nil)
	_ releases.Store   = (*ReleaseStore)(nil)
)

// ---------------------------------------------------------------
// 变更集
// ---------------------------------------------------------------

const changesetSelect = `
	SELECT c.id::text, c.org_id::text, c.author_id::text, u.email, c.state::text, c.title, c.description,
	       c.base_revisions, coalesce(c.submitted_digest, ''), c.fast_track, coalesce(c.release_id::text, ''),
	       c.etag::text, c.created_at, c.updated_at
	FROM changesets c JOIN users u ON u.id = c.author_id`

func scanChangeset(row pgx.Row) (changesets.Changeset, error) {
	var cs changesets.Changeset
	var state string
	var base []byte
	err := row.Scan(&cs.ID, &cs.OrgID, &cs.AuthorID, &cs.AuthorEmail, &state, &cs.Title, &cs.Description,
		&base, &cs.SubmittedDigest, &cs.FastTrack, &cs.ReleaseID, &cs.ETag, &cs.CreatedAt, &cs.UpdatedAt)
	if err != nil {
		return cs, err
	}
	cs.State = changesets.State(state)
	cs.BaseRevisions = map[string]string{}
	_ = json.Unmarshal(base, &cs.BaseRevisions)
	return cs, nil
}

func (s *ChangesetStore) Create(ctx context.Context, cs changesets.Changeset) (string, error) {
	base, _ := json.Marshal(cs.BaseRevisions)
	var id string
	err := pg.WithTx(ctx, s.db.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO changesets (org_id, author_id, state, title, description, base_revisions, fast_track)
			VALUES ($1, $2, 'draft', $3, $4, $5, $6) RETURNING id::text`,
			cs.OrgID, cs.AuthorID, cs.Title, cs.Description, base, cs.FastTrack).Scan(&id); err != nil {
			return err
		}
		return insertOps(ctx, tx, id, cs.Ops)
	})
	return id, err
}

func insertOps(ctx context.Context, tx pgx.Tx, csID string, ops []changesets.Op) error {
	for _, op := range ops {
		var files any
		if op.Op == changesets.OpPut {
			b, _ := json.Marshal(resources.CanonicalRefs(op.Files))
			files = b
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO changeset_operations (changeset_id, seq, op, level, team_id, project_id, kind, name, files, expected_prev_version)
			VALUES ($1, $2, $3::changeset_op, $4::owner_level, NULLIF($5, '')::uuid, NULLIF($6, '')::uuid, $7::bundle_kind, $8, $9, $10)`,
			csID, op.Seq, op.Op, op.Level, op.TeamID, op.ProjectID, op.Kind, op.Name, files, op.ExpectedPrevVersion); err != nil {
			return fmt.Errorf("操作 %d: %w", op.Seq, err)
		}
	}
	return nil
}

func (s *ChangesetStore) Get(ctx context.Context, orgID, id string) (changesets.Changeset, error) {
	cs, err := scanChangeset(s.db.pool.QueryRow(ctx, changesetSelect+` WHERE c.org_id = $1 AND c.id = $2`, orgID, id))
	if isNoRows(err) {
		return cs, changesets.ErrNotFound
	}
	if err != nil {
		return cs, err
	}
	cs.Ops, err = s.loadOps(ctx, id)
	if err != nil {
		return cs, err
	}
	rows, err := s.db.pool.Query(ctx, `
		SELECT r.id::text, r.reviewer_id::text, u.email, r.decision::text, r.digest, r.superseded, r.comment, r.created_at
		FROM changeset_reviews r JOIN users u ON u.id = r.reviewer_id
		WHERE r.changeset_id = $1 ORDER BY r.created_at`, id)
	if err != nil {
		return cs, err
	}
	defer rows.Close()
	cs.Reviews = []changesets.Review{}
	for rows.Next() {
		var r changesets.Review
		var d string
		if err := rows.Scan(&r.ID, &r.ReviewerID, &r.ReviewerEmail, &d, &r.Digest, &r.Superseded, &r.Comment, &r.CreatedAt); err != nil {
			return cs, err
		}
		r.Decision = changesets.Decision(d)
		cs.Reviews = append(cs.Reviews, r)
	}
	return cs, rows.Err()
}

func (s *ChangesetStore) loadOps(ctx context.Context, csID string) ([]changesets.Op, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT seq, op::text, level::text, coalesce(team_id::text, ''), coalesce(project_id::text, ''),
		       kind::text, name, files, expected_prev_version
		FROM changeset_operations WHERE changeset_id = $1 ORDER BY seq`, csID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ops := []changesets.Op{}
	for rows.Next() {
		var op changesets.Op
		var opKind, level, kind string
		var files []byte
		if err := rows.Scan(&op.Seq, &opKind, &level, &op.TeamID, &op.ProjectID, &kind, &op.Name, &files, &op.ExpectedPrevVersion); err != nil {
			return nil, err
		}
		op.Op, op.Level, op.Kind = changesets.OpKind(opKind), resources.Level(level), resources.Kind(kind)
		if files != nil {
			_ = json.Unmarshal(files, &op.Files)
		}
		ops = append(ops, op)
	}
	return ops, rows.Err()
}

func (s *ChangesetStore) List(ctx context.Context, orgID string, f changesets.ListFilter) ([]changesets.Changeset, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.pool.Query(ctx, changesetSelect+`
		WHERE c.org_id = $1
		  AND ($2 = '' OR c.state::text = $2)
		  AND ($3 = '' OR c.author_id::text = $3)
		  AND ($4 = '' OR (c.state = 'in_review' AND c.author_id::text <> $4))
		ORDER BY c.updated_at DESC LIMIT $5`, orgID, string(f.State), f.AuthorID, f.ReviewableBy, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []changesets.Changeset{}
	for rows.Next() {
		cs, err := scanChangeset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Ops, err = s.loadOps(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *ChangesetStore) Update(ctx context.Context, cs changesets.Changeset, ifMatch string) error {
	return pg.WithTx(ctx, s.db.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE changesets SET title = $3, description = $4, fast_track = $5, state = $6::changeset_state,
			       submitted_digest = NULL, etag = gen_random_uuid(), updated_at = clock_timestamp()
			WHERE org_id = $1 AND id = $2 AND etag::text = $7`,
			cs.OrgID, cs.ID, cs.Title, cs.Description, cs.FastTrack, cs.State, ifMatch)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM changesets WHERE org_id = $1 AND id = $2)`, cs.OrgID, cs.ID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return changesets.ErrNotFound
			}
			return changesets.ErrETagMismatch
		}
		if _, err := tx.Exec(ctx, `DELETE FROM changeset_operations WHERE changeset_id = $1`, cs.ID); err != nil {
			return err
		}
		return insertOps(ctx, tx, cs.ID, cs.Ops)
	})
}

func (s *ChangesetStore) SetState(ctx context.Context, orgID, id string, state changesets.State, digest *string) error {
	tag, err := s.db.pool.Exec(ctx, `
		UPDATE changesets SET state = $3::changeset_state,
		       submitted_digest = coalesce($4, submitted_digest),
		       etag = gen_random_uuid(), updated_at = clock_timestamp()
		WHERE org_id = $1 AND id = $2`, orgID, id, state, digest)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return changesets.ErrNotFound
	}
	return nil
}

func (s *ChangesetStore) AddReview(ctx context.Context, orgID, changesetID string, r changesets.Review) error {
	_, err := s.db.pool.Exec(ctx, `
		INSERT INTO changeset_reviews (changeset_id, reviewer_id, decision, digest, comment)
		SELECT c.id, $3, $4::review_decision, $5, $6 FROM changesets c WHERE c.org_id = $1 AND c.id = $2`,
		orgID, changesetID, r.ReviewerID, r.Decision, r.Digest, r.Comment)
	return err
}

func (s *ChangesetStore) SupersedeReviews(ctx context.Context, orgID, changesetID string) error {
	_, err := s.db.pool.Exec(ctx, `
		UPDATE changeset_reviews r SET superseded = true
		FROM changesets c WHERE r.changeset_id = c.id AND c.org_id = $1 AND c.id = $2 AND NOT r.superseded`, orgID, changesetID)
	return err
}

func (s *ChangesetStore) MissingBlobs(ctx context.Context, orgID string, shas []string) ([]string, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT want FROM unnest($2::text[]) AS want
		WHERE NOT EXISTS (SELECT 1 FROM blobs b WHERE b.org_id = $1 AND b.sha256 = want)`, orgID, shas)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (s *ChangesetStore) GetBlob(ctx context.Context, orgID, sha string) ([]byte, error) {
	var b []byte
	err := s.db.pool.QueryRow(ctx, `SELECT bytes FROM blobs WHERE org_id = $1 AND sha256 = $2`, orgID, sha).Scan(&b)
	if isNoRows(err) {
		return nil, changesets.ErrMissingBlob
	}
	return b, err
}

func (s *ChangesetStore) HeadRevisions(ctx context.Context, orgID string, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		level, owner := splitKey(k)
		var rev string
		err := s.db.pool.QueryRow(ctx, `
			SELECT revision FROM content_heads WHERE org_id = $1 AND level = $2::owner_level AND owner_id = $3::uuid`,
			orgID, level, owner).Scan(&rev)
		if isNoRows(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[k] = rev
	}
	return out, nil
}

func (s *ChangesetStore) ScopeExists(ctx context.Context, orgID string, sc organizations.Scope) (bool, error) {
	var ok bool
	var err error
	switch sc.Level {
	case resources.LevelOrg:
		return sc.OwnerID == orgID, nil
	case resources.LevelTeam:
		err = s.db.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM teams WHERE org_id = $1 AND id = $2 AND NOT archived)`, orgID, sc.OwnerID).Scan(&ok)
	case resources.LevelProject:
		err = s.db.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE org_id = $1 AND id = $2 AND NOT archived)`, orgID, sc.OwnerID).Scan(&ok)
	}
	return ok, err
}

func (s *ChangesetStore) CurrentVersion(ctx context.Context, orgID string, sc organizations.Scope, kind resources.Kind, name string) (int, bool, error) {
	var v int
	var deleted bool
	err := s.db.pool.QueryRow(ctx, `
		SELECT v.version, v.deleted FROM bundles b
		JOIN bundle_versions v ON v.bundle_id = b.id
		WHERE b.org_id = $1 AND b.level = $2::owner_level AND b.owner_id = $3::uuid AND b.kind = $4::bundle_kind AND b.name = $5
		ORDER BY v.version DESC LIMIT 1`, orgID, sc.Level, sc.OwnerID, kind, name).Scan(&v, &deleted)
	if isNoRows(err) {
		return 0, false, nil
	}
	return v, deleted, err
}

// ---------------------------------------------------------------
// 发布事务（docs/设计-变更集与发布.md §5）
// ---------------------------------------------------------------

func (s *ReleaseStore) Publish(ctx context.Context, cs changesets.Changeset, publisherID string, now time.Time) (releases.Release, error) {
	var rel releases.Release
	err := pg.WithTx(ctx, s.db.pool, func(tx pgx.Tx) error {
		// 0. 再次确认状态：并发的两次 publish 只有一个能成功
		var state string
		if err := tx.QueryRow(ctx, `SELECT state::text FROM changesets WHERE org_id = $1 AND id = $2 FOR UPDATE`, cs.OrgID, cs.ID).Scan(&state); err != nil {
			return err
		}
		if state != string(changesets.StateApproved) {
			return releases.ErrState
		}

		// 1. 按 scope key 排序锁 head；缺的先建空行，保证锁顺序一致、不会死锁
		keys := make([]string, 0, len(cs.BaseRevisions))
		for k := range cs.BaseRevisions {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		heads := map[string]string{}
		for _, k := range keys {
			level, owner := splitKey(k)
			if _, err := tx.Exec(ctx, `
				INSERT INTO content_heads (org_id, level, owner_id, revision) VALUES ($1, $2::owner_level, $3::uuid, '')
				ON CONFLICT (level, owner_id) DO NOTHING`, cs.OrgID, level, owner); err != nil {
				return err
			}
			var rev string
			if err := tx.QueryRow(ctx, `
				SELECT revision FROM content_heads WHERE level = $1::owner_level AND owner_id = $2::uuid FOR UPDATE`, level, owner).Scan(&rev); err != nil {
				return err
			}
			heads[k] = rev
		}

		// 2. 核对基线；过期的落点看重叠
		var conflicts []releases.Conflict
		for _, k := range keys {
			if heads[k] == cs.BaseRevisions[k] {
				continue
			}
			touched, err := keysChangedSince(ctx, tx, cs.OrgID, k, cs.BaseRevisions[k])
			if err != nil {
				return err
			}
			var overlap []string
			for _, op := range cs.Ops {
				if changesets.ScopeKey(op.Scope(cs.OrgID)) != k {
					continue
				}
				rk := string(op.Kind) + "/" + op.Name
				if touched[rk] {
					overlap = append(overlap, rk)
				}
			}
			if len(overlap) > 0 {
				conflicts = append(conflicts, releases.Conflict{ScopeKey: k, Keys: overlap})
			}
		}
		if len(conflicts) > 0 {
			return &releases.ErrRevisionMismatch{Conflicts: conflicts}
		}

		// 3. 逐操作写版本
		var items []releases.Item
		for _, op := range cs.Ops {
			sc := op.Scope(cs.OrgID)
			teamID, projectID := "", ""
			switch op.Level {
			case resources.LevelTeam:
				teamID = op.TeamID
			case resources.LevelProject:
				projectID = op.ProjectID
			}
			var bundleID string
			if err := tx.QueryRow(ctx, `
				INSERT INTO bundles (org_id, level, team_id, project_id, kind, name)
				VALUES ($1, $2::owner_level, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, $5::bundle_kind, $6)
				ON CONFLICT (org_id, level, owner_id, kind, name) DO UPDATE SET updated_at = now(), archived = false
				RETURNING id::text`, cs.OrgID, op.Level, teamID, projectID, op.Kind, op.Name).Scan(&bundleID); err != nil {
				return err
			}
			var cur int
			var curDeleted bool
			if err := tx.QueryRow(ctx, `
				SELECT coalesce(max(version), 0), coalesce(bool_or(deleted) FILTER (WHERE version = (SELECT max(version) FROM bundle_versions WHERE bundle_id = $1)), false)
				FROM bundle_versions WHERE bundle_id = $1`, bundleID).Scan(&cur, &curDeleted); err != nil {
				return err
			}
			if op.ExpectedPrevVersion != nil && *op.ExpectedPrevVersion != cur {
				return &releases.ErrRevisionMismatch{Conflicts: []releases.Conflict{{ScopeKey: changesets.ScopeKey(sc), Keys: []string{string(op.Kind) + "/" + op.Name}}}}
			}
			next := cur + 1
			item := releases.Item{BundleID: bundleID, Kind: op.Kind, Name: op.Name, Version: next, ScopeKey: changesets.ScopeKey(sc)}
			switch op.Op {
			case changesets.OpPut:
				refs := resources.CanonicalRefs(op.Files)
				sum, err := resources.Checksum(refs)
				if err != nil {
					return err
				}
				fj, _ := json.Marshal(refs)
				if _, err := tx.Exec(ctx, `
					INSERT INTO bundle_versions (bundle_id, version, files, checksum, published_by, published_at)
					VALUES ($1, $2, $3, $4, $5, $6)`, bundleID, next, fj, sum, publisherID, now); err != nil {
					return err
				}
			case changesets.OpDelete:
				if cur == 0 || curDeleted {
					return fmt.Errorf("%w: %s/%s 不存在，无法删除", changesets.ErrInvalidInput, op.Kind, op.Name)
				}
				item.Deleted = true
				if _, err := tx.Exec(ctx, `
					INSERT INTO bundle_versions (bundle_id, version, files, checksum, deleted, published_by, published_at)
					VALUES ($1, $2, '[]'::jsonb, 'tombstone', true, $3, $4)`, bundleID, next, publisherID, now); err != nil {
					return err
				}
			}
			items = append(items, item)
		}

		// 4. 每个落点的新 revision
		revisions := map[string]string{}
		for _, k := range keys {
			level, owner := splitKey(k)
			rev, err := scopeRevision(ctx, tx, cs.OrgID, level, owner)
			if err != nil {
				return err
			}
			revisions[k] = rev
		}
		itemsJSON, _ := json.Marshal(items)
		revJSON, _ := json.Marshal(revisions)
		var relID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO releases (org_id, changeset_id, published_by, published_at, items, revisions)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id::text`,
			cs.OrgID, cs.ID, publisherID, now, itemsJSON, revJSON).Scan(&relID); err != nil {
			return err
		}
		for _, k := range keys {
			level, owner := splitKey(k)
			if _, err := tx.Exec(ctx, `
				UPDATE content_heads SET revision = $3, release_id = $4, updated_at = $5
				WHERE level = $1::owner_level AND owner_id = $2::uuid`, level, owner, revisions[k], relID, now); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE bundle_versions SET release_id = $1 WHERE published_at = $2 AND published_by = $3 AND release_id IS NULL`, relID, now, publisherID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE changesets SET state = 'published', release_id = $3, etag = gen_random_uuid(), updated_at = clock_timestamp()
			WHERE org_id = $1 AND id = $2`, cs.OrgID, cs.ID, relID); err != nil {
			return err
		}
		rel = releases.Release{ID: relID, OrgID: cs.OrgID, ChangesetID: cs.ID, PublishedBy: publisherID, PublishedAt: now, Items: items, Revisions: revisions}
		return nil
	})
	return rel, err
}

// keysChangedSince 返回某落点在 baseRevision 之后的 release 触碰过的资源键（kind/name）。
// baseRevision 为空表示该落点之前没有 head：其后的全部 release 都算。
func keysChangedSince(ctx context.Context, tx pgx.Tx, orgID, scopeKey, baseRevision string) (map[string]bool, error) {
	var since time.Time
	if baseRevision != "" {
		err := tx.QueryRow(ctx, `
			SELECT published_at FROM releases WHERE org_id = $1 AND revisions->>$2 = $3 ORDER BY published_at DESC LIMIT 1`,
			orgID, scopeKey, baseRevision).Scan(&since)
		if err != nil && !isNoRows(err) {
			return nil, err
		}
	}
	rows, err := tx.Query(ctx, `
		SELECT items FROM releases WHERE org_id = $1 AND revisions ? $2 AND published_at > $3`, orgID, scopeKey, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	touched := map[string]bool{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var items []releases.Item
		_ = json.Unmarshal(raw, &items)
		for _, it := range items {
			if it.ScopeKey == scopeKey {
				touched[string(it.Kind)+"/"+it.Name] = true
			}
		}
	}
	return touched, rows.Err()
}

// scopeRevision 对某落点当前生效的全部 (kind, name, version, deleted) 排序后做 SHA-256。
func scopeRevision(ctx context.Context, tx pgx.Tx, orgID, level, owner string) (string, error) {
	rows, err := tx.Query(ctx, `
		SELECT b.kind::text, b.name, v.version, v.deleted
		FROM bundles b
		JOIN LATERAL (SELECT version, deleted FROM bundle_versions WHERE bundle_id = b.id ORDER BY version DESC LIMIT 1) v ON true
		WHERE b.org_id = $1 AND b.level = $2::owner_level AND b.owner_id = $3::uuid
		ORDER BY b.kind, b.name`, orgID, level, owner)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var sb strings.Builder
	for rows.Next() {
		var kind, name string
		var version int
		var deleted bool
		if err := rows.Scan(&kind, &name, &version, &deleted); err != nil {
			return "", err
		}
		fmt.Fprintf(&sb, "%s/%s@%d:%t\n", kind, name, version, deleted)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func splitKey(k string) (string, string) {
	i := strings.IndexByte(k, ':')
	if i < 0 {
		return "", k
	}
	return k[:i], k[i+1:]
}

// ---------------------------------------------------------------
// release 读取
// ---------------------------------------------------------------

const releaseSelect = `
	SELECT r.id::text, r.org_id::text, r.changeset_id::text, coalesce(r.published_by::text, ''), coalesce(u.email, ''),
	       r.published_at, r.items, r.revisions
	FROM releases r LEFT JOIN users u ON u.id = r.published_by`

func scanRelease(row pgx.Row) (releases.Release, error) {
	var r releases.Release
	var items, revs []byte
	err := row.Scan(&r.ID, &r.OrgID, &r.ChangesetID, &r.PublishedBy, &r.PublisherEmail, &r.PublishedAt, &items, &revs)
	if err != nil {
		return r, err
	}
	_ = json.Unmarshal(items, &r.Items)
	_ = json.Unmarshal(revs, &r.Revisions)
	return r, nil
}

func (s *ReleaseStore) Get(ctx context.Context, orgID, id string) (releases.Release, error) {
	r, err := scanRelease(s.db.pool.QueryRow(ctx, releaseSelect+` WHERE r.org_id = $1 AND r.id = $2`, orgID, id))
	if isNoRows(err) {
		return r, releases.ErrNotFound
	}
	return r, err
}

func (s *ReleaseStore) List(ctx context.Context, orgID, scopeKey string, limit int) ([]releases.Release, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.pool.Query(ctx, releaseSelect+`
		WHERE r.org_id = $1 AND ($2 = '' OR r.revisions ? $2)
		ORDER BY r.published_at DESC LIMIT $3`, orgID, scopeKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []releases.Release{}
	for rows.Next() {
		r, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *ReleaseStore) PreviousFiles(ctx context.Context, orgID, bundleID string, version int) ([]resources.FileRef, int, bool, error) {
	var files []byte
	var prev int
	var deleted bool
	err := s.db.pool.QueryRow(ctx, `
		SELECT v.files, v.version, v.deleted FROM bundle_versions v JOIN bundles b ON b.id = v.bundle_id
		WHERE b.org_id = $1 AND v.bundle_id = $2 AND v.version < $3 ORDER BY v.version DESC LIMIT 1`,
		orgID, bundleID, version).Scan(&files, &prev, &deleted)
	if isNoRows(err) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	var refs []resources.FileRef
	_ = json.Unmarshal(files, &refs)
	return refs, prev, deleted, nil
}
