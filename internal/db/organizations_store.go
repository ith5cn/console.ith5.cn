package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/platform/pg"
	"github.com/ith5/ith5/internal/resources"
)

// OrganizationsStore 实现 organizations.Store。
type OrganizationsStore struct{ db *DB }

// Organizations 返回组织模块的仓储。
func (d *DB) Organizations() *OrganizationsStore { return &OrganizationsStore{db: d} }

var _ organizations.Store = (*OrganizationsStore)(nil)

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (s *OrganizationsStore) GetOrganization(ctx context.Context, orgID string) (organizations.Organization, error) {
	var o organizations.Organization
	err := s.db.pool.QueryRow(ctx, `SELECT id::text, name, slug, created_at FROM organizations WHERE id = $1`, orgID).
		Scan(&o.ID, &o.Name, &o.Slug, &o.CreatedAt)
	if isNoRows(err) {
		return o, organizations.ErrNotFound
	}
	return o, err
}

func (s *OrganizationsStore) GetOrganizationBySlug(ctx context.Context, slug string) (organizations.Organization, error) {
	var o organizations.Organization
	err := s.db.pool.QueryRow(ctx, `SELECT id::text, name, slug, created_at FROM organizations WHERE slug = $1`, slug).
		Scan(&o.ID, &o.Name, &o.Slug, &o.CreatedAt)
	if isNoRows(err) {
		return o, organizations.ErrNotFound
	}
	return o, err
}

// CreateOrganization 在一个事务里建组织、默认策略与 owner 成员。
// local 账号的密码哈希存在 users 上，新组织的成员行从该账号已有的任一成员行复制，登录才不会断。
func (s *OrganizationsStore) CreateOrganization(ctx context.Context, accountID, name, slug string) (organizations.Organization, string, error) {
	var o organizations.Organization
	var userID string
	err := pg.WithTx(ctx, s.db.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO organizations (name, slug) VALUES ($1, $2) RETURNING id::text, name, slug, created_at`, name, slug).
			Scan(&o.ID, &o.Name, &o.Slug, &o.CreatedAt)
		if err != nil {
			if isUniqueViolation(err) {
				return organizations.ErrSlugTaken
			}
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO org_policies (org_id) VALUES ($1)`, o.ID); err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO users (org_id, account_id, email, name, role, status, password_hash)
			SELECT $1, a.id, a.email, a.name, 'owner', 'active',
			       (SELECT u.password_hash FROM users u WHERE u.account_id = a.id AND u.password_hash IS NOT NULL ORDER BY u.created_at LIMIT 1)
			FROM accounts a WHERE a.id = $2 AND a.disabled_at IS NULL
			RETURNING id::text`, o.ID, accountID).Scan(&userID)
		if isNoRows(err) {
			return organizations.ErrNotFound
		}
		return err
	})
	return o, userID, err
}

func (s *OrganizationsStore) UpdateOrganization(ctx context.Context, orgID, name string) error {
	_, err := s.db.pool.Exec(ctx, `UPDATE organizations SET name = $2 WHERE id = $1`, orgID, name)
	return err
}

func (s *OrganizationsStore) GetPolicy(ctx context.Context, orgID string) (organizations.Policy, error) {
	p := organizations.Policy{OrgID: orgID}
	err := s.db.pool.QueryRow(ctx, `
		SELECT required_approvals, learnings_review, confidence_prune, confidence_promote, retention_months
		FROM org_policies WHERE org_id = $1`, orgID).
		Scan(&p.RequiredApprovals, &p.LearningsReview, &p.ConfidencePrune, &p.ConfidencePromote, &p.RetentionMonths)
	if isNoRows(err) {
		return p, organizations.ErrNotFound
	}
	return p, err
}

func (s *OrganizationsStore) PutPolicy(ctx context.Context, p organizations.Policy) error {
	_, err := s.db.pool.Exec(ctx, `
		INSERT INTO org_policies (org_id, required_approvals, learnings_review, confidence_prune, confidence_promote, retention_months, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (org_id) DO UPDATE SET
			required_approvals = EXCLUDED.required_approvals, learnings_review = EXCLUDED.learnings_review,
			confidence_prune = EXCLUDED.confidence_prune, confidence_promote = EXCLUDED.confidence_promote,
			retention_months = EXCLUDED.retention_months, updated_at = now()`,
		p.OrgID, p.RequiredApprovals, p.LearningsReview, p.ConfidencePrune, p.ConfidencePromote, p.RetentionMonths)
	return err
}

// ---------------------------------------------------------------
// 团队
// ---------------------------------------------------------------

func (s *OrganizationsStore) ListTeams(ctx context.Context, orgID string) ([]organizations.Team, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT id::text, org_id::text, name, slug, archived, created_at
		FROM teams WHERE org_id = $1 ORDER BY archived, name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []organizations.Team{}
	for rows.Next() {
		var t organizations.Team
		if err := rows.Scan(&t.ID, &t.OrgID, &t.Name, &t.Slug, &t.Archived, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *OrganizationsStore) GetTeam(ctx context.Context, orgID, teamID string) (organizations.Team, error) {
	var t organizations.Team
	err := s.db.pool.QueryRow(ctx, `
		SELECT id::text, org_id::text, name, slug, archived, created_at
		FROM teams WHERE org_id = $1 AND id = $2`, orgID, teamID).
		Scan(&t.ID, &t.OrgID, &t.Name, &t.Slug, &t.Archived, &t.CreatedAt)
	if isNoRows(err) {
		return t, organizations.ErrNotFound
	}
	if err != nil {
		return t, err
	}
	rows, err := s.db.pool.Query(ctx, `
		SELECT u.id::text, u.email, u.name, tm.role::text
		FROM team_members tm JOIN users u ON u.id = tm.user_id
		WHERE tm.team_id = $1 ORDER BY u.email`, teamID)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	t.Members = []organizations.TeamMember{}
	for rows.Next() {
		var m organizations.TeamMember
		var role string
		if err := rows.Scan(&m.UserID, &m.Email, &m.Name, &role); err != nil {
			return t, err
		}
		m.Role = organizations.MemberRole(role)
		t.Members = append(t.Members, m)
	}
	return t, rows.Err()
}

func (s *OrganizationsStore) CreateTeam(ctx context.Context, t organizations.Team) (string, error) {
	var id string
	err := s.db.pool.QueryRow(ctx, `
		INSERT INTO teams (org_id, name, slug) VALUES ($1, $2, $3) RETURNING id::text`,
		t.OrgID, t.Name, t.Slug).Scan(&id)
	if isUniqueViolation(err) {
		return "", organizations.ErrSlugTaken
	}
	return id, err
}

func (s *OrganizationsStore) UpdateTeam(ctx context.Context, orgID, teamID, name string, archived bool) error {
	tag, err := s.db.pool.Exec(ctx, `
		UPDATE teams SET name = $3, archived = $4 WHERE org_id = $1 AND id = $2`, orgID, teamID, name, archived)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return organizations.ErrNotFound
	}
	return nil
}

func (s *OrganizationsStore) PutTeamMember(ctx context.Context, orgID, teamID, userID string, role organizations.MemberRole) error {
	tag, err := s.db.pool.Exec(ctx, `
		INSERT INTO team_members (team_id, user_id, role)
		SELECT t.id, u.id, $4::member_role FROM teams t, users u
		WHERE t.org_id = $1 AND t.id = $2 AND u.org_id = $1 AND u.id = $3
		ON CONFLICT (team_id, user_id) DO UPDATE SET role = EXCLUDED.role`, orgID, teamID, userID, role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return organizations.ErrNotFound
	}
	return nil
}

func (s *OrganizationsStore) DeleteTeamMember(ctx context.Context, orgID, teamID, userID string) error {
	_, err := s.db.pool.Exec(ctx, `
		DELETE FROM team_members tm USING teams t
		WHERE tm.team_id = t.id AND t.org_id = $1 AND t.id = $2 AND tm.user_id = $3`, orgID, teamID, userID)
	return err
}

// ---------------------------------------------------------------
// 组织成员
// ---------------------------------------------------------------

const memberSelect = `
	SELECT u.id::text, u.account_id::text, u.email, u.name, u.role::text, u.status = 'suspended', u.created_at,
	       (SELECT count(*) FROM machines m WHERE m.user_id = u.id AND m.revoked_at IS NULL),
	       (SELECT max(m.last_seen_at) FROM machines m WHERE m.user_id = u.id)
	FROM users u`

func scanMember(row pgx.Row) (organizations.Member, error) {
	var m organizations.Member
	var role string
	err := row.Scan(&m.UserID, &m.AccountID, &m.Email, &m.Name, &role, &m.Suspended, &m.CreatedAt, &m.Machines, &m.LastSeenAt)
	m.Role = identity.Role(role)
	return m, err
}

func (s *OrganizationsStore) ListMembers(ctx context.Context, orgID string) ([]organizations.Member, error) {
	rows, err := s.db.pool.Query(ctx, memberSelect+` WHERE u.org_id = $1 ORDER BY u.email`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []organizations.Member{}
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *OrganizationsStore) GetMember(ctx context.Context, orgID, userID string) (organizations.Member, error) {
	m, err := scanMember(s.db.pool.QueryRow(ctx, memberSelect+` WHERE u.org_id = $1 AND u.id = $2`, orgID, userID))
	if isNoRows(err) {
		return m, organizations.ErrNotFound
	}
	return m, err
}

func (s *OrganizationsStore) CreateLocalMember(ctx context.Context, orgID, email, name string, role identity.Role, pwHash string) (string, error) {
	var userID string
	err := pg.WithTx(ctx, s.db.pool, func(tx pgx.Tx) error {
		acctID, err := upsertLocalAccount(ctx, tx, email, name)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO users (org_id, account_id, email, name, role, status, password_hash)
			VALUES ($1, $2, $3, $4, $5::org_role, 'active', $6)
			RETURNING id::text`, orgID, acctID, email, name, role, pwHash).Scan(&userID)
		if isUniqueViolation(err) {
			return organizations.ErrEmailTaken
		}
		return err
	})
	return userID, err
}

func (s *OrganizationsStore) SetMemberRole(ctx context.Context, orgID, userID string, role identity.Role) error {
	tag, err := s.db.pool.Exec(ctx, `UPDATE users SET role = $3::org_role WHERE org_id = $1 AND id = $2`, orgID, userID, role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return organizations.ErrNotFound
	}
	return nil
}

func (s *OrganizationsStore) CountOwners(ctx context.Context, orgID string) (int, error) {
	var n int
	err := s.db.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE org_id = $1 AND role = 'owner' AND status = 'active'`, orgID).Scan(&n)
	return n, err
}

func (s *OrganizationsStore) SetPassword(ctx context.Context, orgID, userID, pwHash string) error {
	tag, err := s.db.pool.Exec(ctx, `UPDATE users SET password_hash = $3 WHERE org_id = $1 AND id = $2`, orgID, userID, pwHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return organizations.ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------
// reviewer
// ---------------------------------------------------------------

func (s *OrganizationsStore) ListReviewers(ctx context.Context, orgID string, level resources.Level, ownerID string) ([]organizations.Reviewer, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT r.user_id::text, u.email FROM reviewers r JOIN users u ON u.id = r.user_id
		WHERE r.org_id = $1 AND r.level = $2::owner_level AND r.owner_id = $3 ORDER BY u.email`, orgID, level, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []organizations.Reviewer{}
	for rows.Next() {
		r := organizations.Reviewer{Level: level, OwnerID: ownerID}
		if err := rows.Scan(&r.UserID, &r.Email); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *OrganizationsStore) PutReviewers(ctx context.Context, orgID string, level resources.Level, ownerID string, userIDs []string) error {
	return pg.WithTx(ctx, s.db.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM reviewers WHERE org_id = $1 AND level = $2::owner_level AND owner_id = $3`, orgID, level, ownerID); err != nil {
			return err
		}
		for _, uid := range userIDs {
			if _, err := tx.Exec(ctx, `
				INSERT INTO reviewers (org_id, level, owner_id, user_id) VALUES ($1, $2::owner_level, $3, $4)
				ON CONFLICT DO NOTHING`, orgID, level, ownerID, uid); err != nil {
				return fmt.Errorf("reviewer %s: %w", uid, err)
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------
// 权限主体
// ---------------------------------------------------------------

func (s *OrganizationsStore) LoadSubject(ctx context.Context, userID string) (organizations.Subject, error) {
	sub := organizations.Subject{
		UserID: userID, TeamRoles: map[string]organizations.MemberRole{},
		ProjectRoles: map[string]organizations.MemberRole{}, ProjectTeam: map[string]string{}, Reviewer: map[string]bool{},
	}
	var role string
	err := s.db.pool.QueryRow(ctx, `SELECT org_id::text, role::text FROM users WHERE id = $1`, userID).Scan(&sub.OrgID, &role)
	if isNoRows(err) {
		return sub, organizations.ErrNotFound
	}
	if err != nil {
		return sub, err
	}
	sub.OrgRole = identity.Role(role)

	rows, err := s.db.pool.Query(ctx, `SELECT team_id::text, role::text FROM team_members WHERE user_id = $1`, userID)
	if err != nil {
		return sub, err
	}
	for rows.Next() {
		var id, r string
		if err := rows.Scan(&id, &r); err != nil {
			rows.Close()
			return sub, err
		}
		sub.TeamRoles[id] = organizations.MemberRole(r)
	}
	rows.Close()

	rows, err = s.db.pool.Query(ctx, `SELECT project_id::text, role::text FROM project_members WHERE user_id = $1`, userID)
	if err != nil {
		return sub, err
	}
	for rows.Next() {
		var id, r string
		if err := rows.Scan(&id, &r); err != nil {
			rows.Close()
			return sub, err
		}
		sub.ProjectRoles[id] = organizations.MemberRole(r)
	}
	rows.Close()

	// 项目所属团队：只取组织内的，供「团队成员可读其项目」的判定
	rows, err = s.db.pool.Query(ctx, `SELECT id::text, team_id::text FROM projects WHERE org_id = $1 AND team_id IS NOT NULL`, sub.OrgID)
	if err != nil {
		return sub, err
	}
	for rows.Next() {
		var pid, tid string
		if err := rows.Scan(&pid, &tid); err != nil {
			rows.Close()
			return sub, err
		}
		sub.ProjectTeam[pid] = tid
	}
	rows.Close()

	rows, err = s.db.pool.Query(ctx, `SELECT level::text, owner_id::text FROM reviewers WHERE user_id = $1`, userID)
	if err != nil {
		return sub, err
	}
	defer rows.Close()
	for rows.Next() {
		var level, owner string
		if err := rows.Scan(&level, &owner); err != nil {
			return sub, err
		}
		sub.Reviewer[level+":"+owner] = true
	}
	return sub, rows.Err()
}
