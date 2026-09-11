package db

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/ith5/ith5/internal/audit"
)

// AuditStore 实现 audit.Store。
type AuditStore struct{ db *DB }

// Audit 返回审计仓储。
func (d *DB) Audit() *AuditStore { return &AuditStore{db: d} }

var _ audit.Store = (*AuditStore)(nil)

func (s *AuditStore) Record(ctx context.Context, e audit.Event) error {
	detail, err := json.Marshal(e.Detail)
	if err != nil {
		return err
	}
	if e.Detail == nil {
		detail = []byte("{}")
	}
	_, err = s.db.pool.Exec(ctx, `
		INSERT INTO audit_events (org_id, actor_user_id, type, target_type, target_id, detail, request_id, occurred_at)
		VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7, coalesce($8, now()))`,
		e.OrgID, e.ActorUserID, e.Type, e.TargetType, e.TargetID, detail, e.RequestID, nullTime(e.OccurredAt))
	return err
}

// List 按时间倒序分页。游标是「occurred_at|id」的 base64，签名与快照语义留到后续阶段。
func (s *AuditStore) List(ctx context.Context, q audit.Query) ([]audit.Event, string, error) {
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var curTime time.Time
	var curID string
	if q.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		if err != nil {
			return nil, "", audit.ErrBadCursor
		}
		parts := strings.SplitN(string(raw), "|", 2)
		if len(parts) != 2 {
			return nil, "", audit.ErrBadCursor
		}
		ns, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, "", audit.ErrBadCursor
		}
		curTime, curID = time.Unix(0, ns), parts[1]
	}
	rows, err := s.db.pool.Query(ctx, `
		SELECT a.id::text, a.org_id::text, coalesce(a.actor_user_id::text, ''), coalesce(u.email, ''),
		       a.type, a.target_type, a.target_id, a.detail, a.request_id, a.occurred_at
		FROM audit_events a LEFT JOIN users u ON u.id = a.actor_user_id
		WHERE a.org_id = $1
		  AND ($2 = '' OR a.type LIKE $2 || '%')
		  AND ($3 = '' OR a.actor_user_id::text = $3)
		  AND ($4::timestamptz IS NULL OR a.occurred_at >= $4)
		  AND ($5::timestamptz IS NULL OR a.occurred_at < $5)
		  AND ($6::timestamptz IS NULL OR (a.occurred_at, a.id::text) < ($6, $7))
		ORDER BY a.occurred_at DESC, a.id DESC
		LIMIT $8`, q.OrgID, q.Type, q.Actor, nullTime(q.From), nullTime(q.To), nullTime(curTime), curID, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []audit.Event{}
	for rows.Next() {
		var e audit.Event
		var detail []byte
		if err := rows.Scan(&e.ID, &e.OrgID, &e.ActorUserID, &e.ActorEmail, &e.Type, &e.TargetType, &e.TargetID,
			&detail, &e.RequestID, &e.OccurredAt); err != nil {
			return nil, "", err
		}
		_ = json.Unmarshal(detail, &e.Detail)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		last := out[limit-1]
		out = out[:limit]
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(last.OccurredAt.UnixNano(), 10) + "|" + last.ID))
	}
	return out, next, nil
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
