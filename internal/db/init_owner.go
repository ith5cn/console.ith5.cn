package db

import (
	"context"
	"fmt"
)

// InitOwner creates or updates the first production owner account.
func (d *DB) InitOwner(ctx context.Context, orgName, orgSlug, email, name, pwHash string) (string, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var orgID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO organizations (name, slug) VALUES ($1, $2)
		ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name
		RETURNING id::text`, orgName, orgSlug).Scan(&orgID); err != nil {
		return "", fmt.Errorf("组织: %w", err)
	}

	var userID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (org_id, email, name, role, status, password_hash)
		VALUES ($1, $2, $3, 'owner', 'active', $4)
		ON CONFLICT (org_id, email) DO UPDATE SET
			name = EXCLUDED.name,
			role = 'owner',
			status = 'active',
			password_hash = EXCLUDED.password_hash
		RETURNING id::text`, orgID, email, name, pwHash).Scan(&userID); err != nil {
		return "", fmt.Errorf("用户: %w", err)
	}

	return userID, tx.Commit(ctx)
}
