package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ith5/ith5/internal/identity"
)

// EnrollmentStore 实现 identity.EnrollmentStore。
type EnrollmentStore struct{ db *DB }

// Enrollments 返回接入码仓储。
func (d *DB) Enrollments() *EnrollmentStore { return &EnrollmentStore{db: d} }

var _ identity.EnrollmentStore = (*EnrollmentStore)(nil)

const enrollmentSelect = `
	SELECT id::text, org_id::text, project_ids::text[], coalesce(created_by::text, ''), code_hash,
	       expires_at, max_uses, used_count, revoked_at, created_at
	FROM enrollments`

func scanEnrollment(row pgx.Row) (identity.Enrollment, error) {
	var e identity.Enrollment
	err := row.Scan(&e.ID, &e.OrgID, &e.ProjectIDs, &e.CreatedBy, &e.CodeHash,
		&e.ExpiresAt, &e.MaxUses, &e.UsedCount, &e.RevokedAt, &e.CreatedAt)
	if e.ProjectIDs == nil {
		e.ProjectIDs = []string{}
	}
	return e, err
}

func (s *EnrollmentStore) CreateEnrollment(ctx context.Context, e identity.Enrollment) (string, error) {
	var id string
	err := s.db.pool.QueryRow(ctx, `
		INSERT INTO enrollments (org_id, project_ids, created_by, code_hash, expires_at, max_uses)
		VALUES ($1, $2::uuid[], NULLIF($3, '')::uuid, $4, $5, $6) RETURNING id::text`,
		e.OrgID, e.ProjectIDs, e.CreatedBy, e.CodeHash, e.ExpiresAt, e.MaxUses).Scan(&id)
	return id, err
}

func (s *EnrollmentStore) GetEnrollmentByHash(ctx context.Context, codeHash string) (identity.Enrollment, error) {
	e, err := scanEnrollment(s.db.pool.QueryRow(ctx, enrollmentSelect+` WHERE code_hash = $1`, codeHash))
	if isNoRows(err) {
		return e, identity.ErrNotFound
	}
	return e, err
}

// GetEnrollment 按 id 读取；orgID 为空时不限组织（设备流里此时尚不知道组织）。
func (s *EnrollmentStore) GetEnrollment(ctx context.Context, orgID, id string) (identity.Enrollment, error) {
	e, err := scanEnrollment(s.db.pool.QueryRow(ctx, enrollmentSelect+` WHERE id = $1 AND ($2 = '' OR org_id::text = $2)`, id, orgID))
	if isNoRows(err) {
		return e, identity.ErrNotFound
	}
	return e, err
}

func (s *EnrollmentStore) ListEnrollments(ctx context.Context, orgID string) ([]identity.Enrollment, error) {
	rows, err := s.db.pool.Query(ctx, enrollmentSelect+` WHERE org_id = $1 ORDER BY created_at DESC LIMIT 200`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []identity.Enrollment{}
	for rows.Next() {
		e, err := scanEnrollment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *EnrollmentStore) RevokeEnrollment(ctx context.Context, orgID, id string, now time.Time) error {
	tag, err := s.db.pool.Exec(ctx, `
		UPDATE enrollments SET revoked_at = $3 WHERE org_id = $1 AND id = $2 AND revoked_at IS NULL`, orgID, id, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return identity.ErrNotFound
	}
	return nil
}

// ConsumeEnrollment 以 compare-and-set 核销：过期、用尽、撤销都不会命中。
func (s *EnrollmentStore) ConsumeEnrollment(ctx context.Context, id string, now time.Time) error {
	tag, err := s.db.pool.Exec(ctx, `
		UPDATE enrollments SET used_count = used_count + 1
		WHERE id = $1 AND revoked_at IS NULL AND expires_at > $2 AND used_count < max_uses`, id, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return identity.ErrEnrollmentUnusable
	}
	return nil
}
