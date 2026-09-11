package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/platform/pg"
)

// OIDCStore 实现 identity.OIDCStore。
//
// client_secret 目前明文存 bytea；接入 secret manager 前先不宣称加密。
type OIDCStore struct{ db *DB }

// OIDC 返回 OIDC 配置仓储。
func (d *DB) OIDC() *OIDCStore { return &OIDCStore{db: d} }

var _ identity.OIDCStore = (*OIDCStore)(nil)

func (s *OIDCStore) GetOIDCConfig(ctx context.Context, orgID string) (identity.OIDCConfig, error) {
	var c identity.OIDCConfig
	var secret []byte
	err := s.db.pool.QueryRow(ctx, `
		SELECT id::text, org_id::text, issuer, client_id, client_secret_enc, scopes, enabled
		FROM idp_providers WHERE org_id = $1 AND kind = 'oidc' ORDER BY created_at LIMIT 1`, orgID).
		Scan(&c.ID, &c.OrgID, &c.Issuer, &c.ClientID, &secret, &c.Scopes, &c.Enabled)
	if isNoRows(err) {
		return c, identity.ErrNotFound
	}
	c.ClientSecret = string(secret)
	return c, err
}

func (s *OIDCStore) PutOIDCConfig(ctx context.Context, c identity.OIDCConfig) (string, error) {
	var id string
	err := s.db.pool.QueryRow(ctx, `
		INSERT INTO idp_providers (org_id, kind, issuer, client_id, client_secret_enc, scopes, enabled)
		VALUES ($1, 'oidc', $2, $3, $4, $5, $6)
		ON CONFLICT (org_id, issuer) DO UPDATE SET
			client_id = EXCLUDED.client_id,
			client_secret_enc = CASE WHEN length(EXCLUDED.client_secret_enc) > 0 THEN EXCLUDED.client_secret_enc ELSE idp_providers.client_secret_enc END,
			scopes = EXCLUDED.scopes, enabled = EXCLUDED.enabled
		RETURNING id::text`, c.OrgID, c.Issuer, c.ClientID, []byte(c.ClientSecret), scopesOrEmpty(c.Scopes), c.Enabled).Scan(&id)
	return id, err
}

func (s *OIDCStore) ListGroupMappings(ctx context.Context, orgID string) ([]identity.GroupMapping, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT id::text, provider_id::text, idp_group, target::text, coalesce(target_id::text, ''), role, priority
		FROM idp_group_mappings WHERE org_id = $1 ORDER BY priority DESC, idp_group`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []identity.GroupMapping{}
	for rows.Next() {
		var m identity.GroupMapping
		if err := rows.Scan(&m.ID, &m.ProviderID, &m.IdPGroup, &m.Target, &m.TargetID, &m.Role, &m.Priority); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *OIDCStore) PutGroupMappings(ctx context.Context, orgID, providerID string, ms []identity.GroupMapping) error {
	return pg.WithTx(ctx, s.db.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM idp_group_mappings WHERE org_id = $1 AND provider_id = $2`, orgID, providerID); err != nil {
			return err
		}
		for _, m := range ms {
			if _, err := tx.Exec(ctx, `
				INSERT INTO idp_group_mappings (org_id, provider_id, idp_group, target, target_id, role, priority)
				VALUES ($1, $2, $3, $4::owner_level, NULLIF($5, '')::uuid, $6, $7)`,
				orgID, providerID, m.IdPGroup, m.Target, m.TargetID, m.Role, m.Priority); err != nil {
				return fmt.Errorf("映射 %s: %w", m.IdPGroup, err)
			}
		}
		return nil
	})
}

// ProvisionOIDCIdentity 按 (issuer, subject) 建档或更新账号，再按映射结果落成员记录与团队 / 项目角色。
//
// 组织角色为空且此前不是成员 → ErrNotFound（调用方翻译成「未被授权进入该组织」）。
// 组织角色为空但已是成员 → 保留既有组织角色；团队 / 项目角色按映射结果覆盖。
func (s *OIDCStore) ProvisionOIDCIdentity(ctx context.Context, orgID string, id identity.Identity, orgRole identity.Role, teamRoles, projectRoles map[string]string) (identity.Membership, error) {
	var userID string
	err := pg.WithTx(ctx, s.db.pool, func(tx pgx.Tx) error {
		var acctID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO accounts (issuer, subject, email, name) VALUES ($1, $2, $3, $4)
			ON CONFLICT (issuer, subject) DO UPDATE SET email = EXCLUDED.email, name = EXCLUDED.name
			RETURNING id::text`, id.Issuer, id.Subject, id.Email, id.Name).Scan(&acctID); err != nil {
			return fmt.Errorf("账号: %w", err)
		}
		err := tx.QueryRow(ctx, `SELECT id::text FROM users WHERE org_id = $1 AND account_id = $2`, orgID, acctID).Scan(&userID)
		switch {
		case isNoRows(err) && orgRole == "":
			return identity.ErrNotFound
		case isNoRows(err):
			if err := tx.QueryRow(ctx, `
				INSERT INTO users (org_id, account_id, email, name, role, status, idp_groups, idp_synced_at)
				VALUES ($1, $2, $3, $4, $5::org_role, 'active', $6, now()) RETURNING id::text`,
				orgID, acctID, id.Email, id.Name, orgRole, id.Groups).Scan(&userID); err != nil {
				return fmt.Errorf("成员: %w", err)
			}
		case err != nil:
			return err
		default:
			if orgRole != "" {
				if _, err := tx.Exec(ctx, `UPDATE users SET role = $2::org_role, email = $3, name = $4 WHERE id = $1`, userID, orgRole, id.Email, id.Name); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE users SET idp_groups = $2, idp_synced_at = now() WHERE id = $1`, userID, id.Groups); err != nil {
				return err
			}
		}
		// 团队 / 项目角色：以映射结果为准，先清再写
		if _, err := tx.Exec(ctx, `DELETE FROM team_members WHERE user_id = $1`, userID); err != nil {
			return err
		}
		for tid, role := range teamRoles {
			if _, err := tx.Exec(ctx, `
				INSERT INTO team_members (team_id, user_id, role)
				SELECT id, $2, $3::member_role FROM teams WHERE id = $1 AND org_id = $4
				ON CONFLICT DO NOTHING`, tid, userID, role, orgID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM project_members WHERE user_id = $1`, userID); err != nil {
			return err
		}
		for pid, role := range projectRoles {
			if _, err := tx.Exec(ctx, `
				INSERT INTO project_members (project_id, user_id, role)
				SELECT id, $2, $3::member_role FROM projects WHERE id = $1 AND org_id = $4
				ON CONFLICT DO NOTHING`, pid, userID, role, orgID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return identity.Membership{}, err
	}
	return s.db.Identity().GetMembership(ctx, userID)
}

var _ identity.ReconcileStore = (*OIDCStore)(nil)

// ListOIDCMembers 列出经 OIDC 登录过（有 idp_groups）的活跃成员及其团队 / 项目角色。
func (s *OIDCStore) ListOIDCMembers(ctx context.Context, orgID string) ([]identity.OIDCMember, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT u.id::text, u.email, u.role::text, u.idp_groups, u.idp_synced_at,
		       coalesce((SELECT jsonb_object_agg(tm.team_id::text, tm.role::text) FROM team_members tm WHERE tm.user_id = u.id), '{}'::jsonb),
		       coalesce((SELECT jsonb_object_agg(pm.project_id::text, pm.role::text) FROM project_members pm WHERE pm.user_id = u.id), '{}'::jsonb)
		FROM users u
		WHERE u.org_id = $1 AND u.status = 'active' AND u.idp_groups IS NOT NULL
		ORDER BY u.email`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []identity.OIDCMember
	for rows.Next() {
		var m identity.OIDCMember
		var role string
		if err := rows.Scan(&m.UserID, &m.Email, &role, &m.Groups, &m.SyncedAt, &m.TeamRoles, &m.ProjectRoles); err != nil {
			return nil, err
		}
		m.Role = identity.Role(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ApplyMembership 按折算结果重写角色，与首次登录时的落库规则一致。
func (s *OIDCStore) ApplyMembership(ctx context.Context, orgID, userID string, orgRole identity.Role, teamRoles, projectRoles map[string]string) error {
	return pg.WithTx(ctx, s.db.pool, func(tx pgx.Tx) error {
		if orgRole != "" {
			if _, err := tx.Exec(ctx, `UPDATE users SET role = $3::org_role WHERE id = $1 AND org_id = $2`, userID, orgID, orgRole); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM team_members WHERE user_id = $1`, userID); err != nil {
			return err
		}
		for tid, role := range teamRoles {
			if _, err := tx.Exec(ctx, `
				INSERT INTO team_members (team_id, user_id, role)
				SELECT id, $2, $3::member_role FROM teams WHERE id = $1 AND org_id = $4
				ON CONFLICT DO NOTHING`, tid, userID, role, orgID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM project_members WHERE user_id = $1`, userID); err != nil {
			return err
		}
		for pid, role := range projectRoles {
			if _, err := tx.Exec(ctx, `
				INSERT INTO project_members (project_id, user_id, role)
				SELECT id, $2, $3::member_role FROM projects WHERE id = $1 AND org_id = $4
				ON CONFLICT DO NOTHING`, pid, userID, role, orgID); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `UPDATE users SET idp_synced_at = now() WHERE id = $1`, userID)
		return err
	})
}

func scopesOrEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
