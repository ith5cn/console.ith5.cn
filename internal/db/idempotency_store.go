package db

import (
	"context"
	"time"

	"github.com/ith5/ith5/internal/idempotency"
)

// IdempotencyStore 实现 idempotency.Store。
type IdempotencyStore struct{ db *DB }

// Idempotency 返回幂等键仓储。
func (d *DB) Idempotency() *IdempotencyStore { return &IdempotencyStore{db: d} }

var _ idempotency.Store = (*IdempotencyStore)(nil)

// Begin 用 INSERT … ON CONFLICT DO NOTHING 抢占键：抢到的是首个请求，没抢到的读回已有记录。
// 过期行顺手清掉，不另起清理任务。
func (s *IdempotencyStore) Begin(ctx context.Context, orgID, userID, key string, rec idempotency.Record, ttl time.Duration) (*idempotency.Record, bool, error) {
	_, _ = s.db.pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE org_id = $1 AND user_id = $2 AND expires_at < now()`, orgID, userID)
	tag, err := s.db.pool.Exec(ctx, `
		INSERT INTO idempotency_keys (org_id, user_id, key, method, route, body_sha256, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, now() + $7::interval)
		ON CONFLICT (org_id, user_id, key) DO NOTHING`,
		orgID, userID, key, rec.Method, rec.Route, rec.BodySHA256, ttl.String())
	if err != nil {
		return nil, false, err
	}
	if tag.RowsAffected() == 1 {
		return nil, true, nil
	}
	var got idempotency.Record
	var status *int
	err = s.db.pool.QueryRow(ctx, `
		SELECT method, route, body_sha256, status, response
		FROM idempotency_keys WHERE org_id = $1 AND user_id = $2 AND key = $3`,
		orgID, userID, key).Scan(&got.Method, &got.Route, &got.BodySHA256, &status, &got.Response)
	if err != nil {
		if isNoRows(err) {
			return nil, false, ErrNotFound
		}
		return nil, false, err
	}
	if status != nil {
		got.Status = *status
	}
	return &got, false, nil
}

// Complete 写回结果。响应体为空或不是合法 JSON 时只记状态码。
func (s *IdempotencyStore) Complete(ctx context.Context, orgID, userID, key string, status int, response []byte) error {
	var resp any
	if len(response) > 0 {
		resp = response
	}
	_, err := s.db.pool.Exec(ctx, `
		UPDATE idempotency_keys SET status = $4, response = $5
		WHERE org_id = $1 AND user_id = $2 AND key = $3`,
		orgID, userID, key, status, resp)
	return err
}
