package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/platform/pg"
)

// SuspendStore 实现 identity.SuspendStore：停用与撤销的传播在一个事务里完成。
type SuspendStore struct{ db *DB }

// Suspend 返回撤销传播仓储。
func (d *DB) Suspend() *SuspendStore { return &SuspendStore{db: d} }

var _ identity.SuspendStore = (*SuspendStore)(nil)

func (s *SuspendStore) SuspendUser(ctx context.Context, orgID, userID string, now time.Time) error {
	return pg.WithTx(ctx, s.db.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE users SET status = 'suspended' WHERE org_id = $1 AND id = $2`, orgID, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return identity.ErrNotFound
		}
		if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`, userID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE machines SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`, userID, now); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE bindings SET state = 'revoked', updated_at = $2 WHERE user_id = $1 AND state <> 'revoked'`, userID, now)
		return err
	})
}

func (s *SuspendStore) ReactivateUser(ctx context.Context, orgID, userID string) error {
	tag, err := s.db.pool.Exec(ctx, `UPDATE users SET status = 'active' WHERE org_id = $1 AND id = $2`, orgID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return identity.ErrNotFound
	}
	return nil
}

func (s *SuspendStore) RevokeMachine(ctx context.Context, orgID, machineID string, now time.Time) error {
	return pg.WithTx(ctx, s.db.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE machines SET revoked_at = $3 WHERE org_id = $1 AND id = $2 AND revoked_at IS NULL`, orgID, machineID, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return identity.ErrNotFound
		}
		if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = $2 WHERE machine_id = $1 AND revoked_at IS NULL`, machineID, now); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE bindings SET state = 'revoked', updated_at = $2 WHERE machine_id = $1 AND state <> 'revoked'`, machineID, now)
		return err
	})
}

func (s *SuspendStore) ListMachines(ctx context.Context, orgID, userID string) ([]identity.MachineInfo, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT m.id::text, m.org_id::text, m.user_id::text, m.fingerprint, m.hostname, m.os, m.revoked_at,
		       u.email, m.last_seen_at, m.created_at
		FROM machines m JOIN users u ON u.id = m.user_id
		WHERE m.org_id = $1 AND ($2 = '' OR m.user_id::text = $2)
		ORDER BY m.last_seen_at DESC`, orgID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []identity.MachineInfo{}
	for rows.Next() {
		var m identity.MachineInfo
		if err := rows.Scan(&m.ID, &m.OrgID, &m.UserID, &m.Fingerprint, &m.Hostname, &m.OS, &m.RevokedAt,
			&m.Email, &m.LastSeenAt, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *SuspendStore) RenameMachine(ctx context.Context, orgID, machineID, userID, hostname string) error {
	tag, err := s.db.pool.Exec(ctx, `
		UPDATE machines SET hostname = $4 WHERE org_id = $1 AND id = $2 AND ($3 = '' OR user_id::text = $3)`,
		orgID, machineID, userID, hostname)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return identity.ErrNotFound
	}
	return nil
}
