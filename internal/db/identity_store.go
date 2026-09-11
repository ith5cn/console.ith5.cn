package db

import (
	"context"
	"time"

	"github.com/ith5/ith5/internal/identity"
)

// IdentityStore 实现 identity.Store。
type IdentityStore struct{ db *DB }

// Identity 返回身份模块的仓储。
func (d *DB) Identity() *IdentityStore { return &IdentityStore{db: d} }

var _ identity.Store = (*IdentityStore)(nil)

func (s *IdentityStore) FindLocalAccount(ctx context.Context, email string) (identity.Account, string, error) {
	var a identity.Account
	var hash *string
	err := s.db.pool.QueryRow(ctx, `
		SELECT a.id::text, a.issuer, a.subject, a.email, a.name, a.disabled_at,
		       (SELECT u.password_hash FROM users u
		         WHERE u.account_id = a.id AND u.password_hash IS NOT NULL
		         ORDER BY u.created_at LIMIT 1)
		FROM accounts a
		WHERE a.issuer = 'local' AND a.subject = $1`, email).
		Scan(&a.ID, &a.Issuer, &a.Subject, &a.Email, &a.Name, &a.DisabledAt, &hash)
	if isNoRows(err) {
		return a, "", identity.ErrNotFound
	}
	if err != nil {
		return a, "", err
	}
	if hash == nil {
		return a, "", nil
	}
	return a, *hash, nil
}

const membershipSelect = `
	SELECT u.id::text, u.account_id::text, u.org_id::text, o.slug, o.name,
	       u.email, u.name, u.role::text, u.status = 'suspended'
	FROM users u JOIN organizations o ON o.id = u.org_id`

func (s *IdentityStore) ListMemberships(ctx context.Context, accountID string) ([]identity.Membership, error) {
	rows, err := s.db.pool.Query(ctx, membershipSelect+` WHERE u.account_id = $1 ORDER BY o.name`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []identity.Membership
	for rows.Next() {
		var m identity.Membership
		var role string
		if err := rows.Scan(&m.UserID, &m.AccountID, &m.OrgID, &m.OrgSlug, &m.OrgName,
			&m.Email, &m.Name, &role, &m.Suspended); err != nil {
			return nil, err
		}
		m.Role = identity.Role(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *IdentityStore) GetMembership(ctx context.Context, userID string) (identity.Membership, error) {
	var m identity.Membership
	var role string
	err := s.db.pool.QueryRow(ctx, membershipSelect+` WHERE u.id = $1`, userID).
		Scan(&m.UserID, &m.AccountID, &m.OrgID, &m.OrgSlug, &m.OrgName,
			&m.Email, &m.Name, &role, &m.Suspended)
	if isNoRows(err) {
		return m, identity.ErrNotFound
	}
	m.Role = identity.Role(role)
	return m, err
}

// ---------------------------------------------------------------
// 设备码
// ---------------------------------------------------------------

func (s *IdentityStore) CreateDeviceCode(ctx context.Context, dc identity.DeviceCode) error {
	_, err := s.db.pool.Exec(ctx, `
		INSERT INTO device_codes (device_code_hash, user_code, fingerprint, hostname, os, enrollment_id, expires_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::uuid, $7)`,
		dc.CodeHash, dc.UserCode, dc.Fingerprint, dc.Hostname, dc.OS, dc.EnrollmentID, dc.ExpiresAt)
	return err
}

func (s *IdentityStore) GetDeviceCodeByUserCode(ctx context.Context, userCode string) (identity.DeviceCode, error) {
	var dc identity.DeviceCode
	err := s.db.pool.QueryRow(ctx, `
		SELECT device_code_hash, user_code, fingerprint, hostname, os, status::text,
		       coalesce(enrollment_id::text, ''), coalesce(user_id::text, ''), coalesce(machine_id::text, ''), expires_at
		FROM device_codes WHERE user_code = $1`, userCode).
		Scan(&dc.CodeHash, &dc.UserCode, &dc.Fingerprint, &dc.Hostname, &dc.OS, &dc.Status,
			&dc.EnrollmentID, &dc.UserID, &dc.MachineID, &dc.ExpiresAt)
	if isNoRows(err) {
		return dc, identity.ErrNotFound
	}
	return dc, err
}

func (s *IdentityStore) ApproveDeviceCode(ctx context.Context, userCode, userID, machineID string, now time.Time) error {
	tag, err := s.db.pool.Exec(ctx, `
		UPDATE device_codes SET status = 'approved', user_id = $2, machine_id = $3
		WHERE user_code = $1 AND status = 'pending' AND expires_at > $4`,
		userCode, userID, machineID, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return identity.ErrNotFound
	}
	return nil
}

func (s *IdentityStore) ConsumeDeviceCode(ctx context.Context, codeHash string, now time.Time) (identity.DeviceCode, error) {
	var dc identity.DeviceCode
	err := s.db.pool.QueryRow(ctx, `
		UPDATE device_codes SET status = 'consumed'
		WHERE device_code_hash = $1 AND status = 'approved' AND expires_at > $2
		RETURNING device_code_hash, user_code, fingerprint, hostname, os, status::text,
		          coalesce(enrollment_id::text, ''), user_id::text, machine_id::text, expires_at`, codeHash, now).
		Scan(&dc.CodeHash, &dc.UserCode, &dc.Fingerprint, &dc.Hostname, &dc.OS, &dc.Status,
			&dc.EnrollmentID, &dc.UserID, &dc.MachineID, &dc.ExpiresAt)
	if isNoRows(err) {
		return dc, identity.ErrNotFound
	}
	return dc, err
}

func (s *IdentityStore) DeviceCodeStatus(ctx context.Context, codeHash string) (string, time.Time, error) {
	var status string
	var exp time.Time
	err := s.db.pool.QueryRow(ctx,
		`SELECT status::text, expires_at FROM device_codes WHERE device_code_hash = $1`, codeHash).
		Scan(&status, &exp)
	if isNoRows(err) {
		return "", exp, identity.ErrNotFound
	}
	return status, exp, err
}

// ---------------------------------------------------------------
// 设备
// ---------------------------------------------------------------

func (s *IdentityStore) UpsertMachine(ctx context.Context, m identity.Machine) (string, error) {
	var id string
	err := s.db.pool.QueryRow(ctx, `
		INSERT INTO machines (org_id, user_id, fingerprint, hostname, os)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, fingerprint) DO UPDATE SET
			hostname = EXCLUDED.hostname, os = EXCLUDED.os,
			revoked_at = NULL, last_seen_at = now()
		RETURNING id::text`, m.OrgID, m.UserID, m.Fingerprint, m.Hostname, m.OS).Scan(&id)
	return id, err
}

func (s *IdentityStore) GetMachine(ctx context.Context, id string) (identity.Machine, error) {
	var m identity.Machine
	err := s.db.pool.QueryRow(ctx, `
		SELECT id::text, org_id::text, user_id::text, fingerprint, hostname, os, revoked_at
		FROM machines WHERE id = $1`, id).
		Scan(&m.ID, &m.OrgID, &m.UserID, &m.Fingerprint, &m.Hostname, &m.OS, &m.RevokedAt)
	if isNoRows(err) {
		return m, identity.ErrNotFound
	}
	return m, err
}

func (s *IdentityStore) TouchMachine(ctx context.Context, id string, now time.Time) error {
	_, err := s.db.pool.Exec(ctx, `UPDATE machines SET last_seen_at = $2 WHERE id = $1`, id, now)
	return err
}

// ---------------------------------------------------------------
// 刷新凭据
// ---------------------------------------------------------------

func (s *IdentityStore) CreateRefreshToken(ctx context.Context, t identity.RefreshToken) error {
	_, err := s.db.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (token_hash, family_id, user_id, machine_id, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, t.TokenHash, t.FamilyID, t.UserID, t.MachineID, t.ExpiresAt)
	return err
}

func (s *IdentityStore) GetRefreshToken(ctx context.Context, tokenHash string) (identity.RefreshToken, error) {
	var t identity.RefreshToken
	err := s.db.pool.QueryRow(ctx, `
		SELECT token_hash, family_id::text, user_id::text, machine_id::text, expires_at, revoked_at
		FROM refresh_tokens WHERE token_hash = $1`, tokenHash).
		Scan(&t.TokenHash, &t.FamilyID, &t.UserID, &t.MachineID, &t.ExpiresAt, &t.RevokedAt)
	if isNoRows(err) {
		return t, identity.ErrNotFound
	}
	return t, err
}

func (s *IdentityStore) RevokeRefreshToken(ctx context.Context, tokenHash string, now time.Time) error {
	_, err := s.db.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = $2 WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash, now)
	return err
}

func (s *IdentityStore) RevokeRefreshFamily(ctx context.Context, familyID string, now time.Time) error {
	_, err := s.db.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`, familyID, now)
	return err
}

func (s *IdentityStore) RevokeMachineTokens(ctx context.Context, userID, machineID string, now time.Time) error {
	_, err := s.db.pool.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = $3
		WHERE user_id = $1 AND machine_id = $2 AND revoked_at IS NULL`, userID, machineID, now)
	return err
}
