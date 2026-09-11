package db

import (
	"context"
	"time"

	"github.com/ith5/ith5/internal/maintenance"
)

// MaintenanceStore 实现 maintenance.Store。
type MaintenanceStore struct{ db *DB }

// Maintenance 返回清理仓储。
func (d *DB) Maintenance() *MaintenanceStore { return &MaintenanceStore{db: d} }

var _ maintenance.Store = (*MaintenanceStore)(nil)

func (s *MaintenanceStore) ListRetention(ctx context.Context) ([]maintenance.Retention, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT o.id::text, coalesce(p.retention_months, 13)
		FROM organizations o LEFT JOIN org_policies p ON p.org_id = o.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []maintenance.Retention
	for rows.Next() {
		var r maintenance.Retention
		if err := rows.Scan(&r.OrgID, &r.Months); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *MaintenanceStore) PurgeTelemetry(ctx context.Context, orgID string, before time.Time) (maintenance.Counts, error) {
	c := maintenance.Counts{}
	stmts := []struct{ table, sql string }{
		{"sessions", `DELETE FROM sessions WHERE org_id = $1 AND coalesce(started_at, received_at) < $2`},
		{"usage_daily", `DELETE FROM usage_daily WHERE org_id = $1 AND day < $2::date`},
		{"execution_events", `DELETE FROM execution_events WHERE org_id = $1 AND occurred_at < $2`},
	}
	for _, st := range stmts {
		tag, err := s.db.pool.Exec(ctx, st.sql, orgID, before)
		if err != nil {
			return c, err
		}
		c[st.table] += tag.RowsAffected()
	}
	return c, nil
}

func (s *MaintenanceStore) PurgeLedgers(ctx context.Context, now time.Time) (maintenance.Counts, error) {
	c := maintenance.Counts{}
	cutoff := now.Add(-maintenance.LedgerWindow)
	stmts := []struct {
		table, sql string
		arg        time.Time
	}{
		{"report_events", `DELETE FROM report_events WHERE received_at < $1`, cutoff},
		{"idempotency_keys", `DELETE FROM idempotency_keys WHERE expires_at < $1`, now},
		{"device_codes", `DELETE FROM device_codes WHERE expires_at < $1`, cutoff},
		{"refresh_tokens", `DELETE FROM refresh_tokens WHERE coalesce(revoked_at, expires_at) < $1`, cutoff},
		{"enrollments", `DELETE FROM enrollments WHERE coalesce(revoked_at, expires_at) < $1`, cutoff},
	}
	for _, st := range stmts {
		tag, err := s.db.pool.Exec(ctx, st.sql, st.arg)
		if err != nil {
			return c, err
		}
		c[st.table] += tag.RowsAffected()
	}
	return c, nil
}
