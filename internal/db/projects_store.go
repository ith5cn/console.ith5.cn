package db

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/projects"
)

// ProjectsStore 实现 projects.Store。
type ProjectsStore struct{ db *DB }

// Projects 返回项目模块的仓储。
func (d *DB) Projects() *ProjectsStore { return &ProjectsStore{db: d} }

var _ projects.Store = (*ProjectsStore)(nil)

const projectSelect = `SELECT id::text, org_id::text, coalesce(team_id::text, ''), name, slug, archived, created_at FROM projects`

func scanProject(row pgx.Row) (projects.Project, error) {
	var p projects.Project
	err := row.Scan(&p.ID, &p.OrgID, &p.TeamID, &p.Name, &p.Slug, &p.Archived, &p.CreatedAt)
	return p, err
}

func (s *ProjectsStore) ListProjects(ctx context.Context, orgID string) ([]projects.Project, error) {
	rows, err := s.db.pool.Query(ctx, projectSelect+` WHERE org_id = $1 ORDER BY archived, name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []projects.Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *ProjectsStore) GetProject(ctx context.Context, orgID, projectID string) (projects.Project, error) {
	p, err := scanProject(s.db.pool.QueryRow(ctx, projectSelect+` WHERE org_id = $1 AND id = $2`, orgID, projectID))
	if isNoRows(err) {
		return p, projects.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	rows, err := s.db.pool.Query(ctx, `
		SELECT u.id::text, u.email, u.name, pm.role::text
		FROM project_members pm JOIN users u ON u.id = pm.user_id
		WHERE pm.project_id = $1 ORDER BY u.email`, projectID)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	p.Members = []projects.Member{}
	for rows.Next() {
		var m projects.Member
		var role string
		if err := rows.Scan(&m.UserID, &m.Email, &m.Name, &role); err != nil {
			return p, err
		}
		m.Role = organizations.MemberRole(role)
		p.Members = append(p.Members, m)
	}
	return p, rows.Err()
}

func (s *ProjectsStore) CreateProject(ctx context.Context, p projects.Project) (string, error) {
	var id string
	err := s.db.pool.QueryRow(ctx, `
		INSERT INTO projects (org_id, team_id, name, slug)
		SELECT $1, NULLIF($2, '')::uuid, $3, $4
		WHERE $2 = '' OR EXISTS (SELECT 1 FROM teams t WHERE t.id = NULLIF($2, '')::uuid AND t.org_id = $1)
		RETURNING id::text`, p.OrgID, p.TeamID, p.Name, p.Slug).Scan(&id)
	if isUniqueViolation(err) {
		return "", projects.ErrSlugTaken
	}
	if isNoRows(err) {
		return "", projects.ErrInvalidInput // 团队不属于本组织
	}
	return id, err
}

func (s *ProjectsStore) UpdateProject(ctx context.Context, orgID, projectID, name, teamID string, archived bool) error {
	tag, err := s.db.pool.Exec(ctx, `
		UPDATE projects SET name = $3, team_id = NULLIF($4, '')::uuid, archived = $5
		WHERE org_id = $1 AND id = $2
		  AND ($4 = '' OR EXISTS (SELECT 1 FROM teams t WHERE t.id = NULLIF($4, '')::uuid AND t.org_id = $1))`,
		orgID, projectID, name, teamID, archived)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return projects.ErrNotFound
	}
	return nil
}

func (s *ProjectsStore) PutProjectMember(ctx context.Context, orgID, projectID, userID string, role organizations.MemberRole) error {
	tag, err := s.db.pool.Exec(ctx, `
		INSERT INTO project_members (project_id, user_id, role)
		SELECT p.id, u.id, $4::member_role FROM projects p, users u
		WHERE p.org_id = $1 AND p.id = $2 AND u.org_id = $1 AND u.id = $3
		ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role`, orgID, projectID, userID, role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return projects.ErrNotFound
	}
	return nil
}

func (s *ProjectsStore) DeleteProjectMember(ctx context.Context, orgID, projectID, userID string) error {
	_, err := s.db.pool.Exec(ctx, `
		DELETE FROM project_members pm USING projects p
		WHERE pm.project_id = p.id AND p.org_id = $1 AND p.id = $2 AND pm.user_id = $3`, orgID, projectID, userID)
	return err
}

// ---------------------------------------------------------------
// 绑定
// ---------------------------------------------------------------

const bindingSelect = `
	SELECT id::text, org_id::text, user_id::text, machine_id::text, workspace_id, display_name,
	       project_ids::text[], coalesce(applied_revision, ''), state::text, created_at, updated_at, last_sync_at,
	       md5(updated_at::text || coalesce(applied_revision, ''))
	FROM bindings`

func scanBinding(row pgx.Row) (projects.Binding, error) {
	var b projects.Binding
	var state string
	err := row.Scan(&b.ID, &b.OrgID, &b.UserID, &b.MachineID, &b.WorkspaceID, &b.DisplayName,
		&b.ProjectIDs, &b.AppliedRevision, &state, &b.CreatedAt, &b.UpdatedAt, &b.LastSyncAt, &b.ETag)
	b.State = projects.BindingState(state)
	if b.ProjectIDs == nil {
		b.ProjectIDs = []string{}
	}
	return b, err
}

// CreateBinding 按 (machine, workspace) 幂等：同一目录再次绑定就是更新项目集。
func (s *ProjectsStore) CreateBinding(ctx context.Context, b projects.Binding) (projects.Binding, error) {
	var id string
	err := s.db.pool.QueryRow(ctx, `
		INSERT INTO bindings (org_id, user_id, machine_id, workspace_id, display_name, project_ids, state)
		VALUES ($1, $2, $3, $4, $5, $6::uuid[], 'active')
		ON CONFLICT (machine_id, workspace_id) DO UPDATE SET
			display_name = EXCLUDED.display_name, project_ids = EXCLUDED.project_ids,
			state = 'active', updated_at = now()
		RETURNING id::text`, b.OrgID, b.UserID, b.MachineID, b.WorkspaceID, b.DisplayName, b.ProjectIDs).Scan(&id)
	if err != nil {
		return projects.Binding{}, err
	}
	return s.GetBinding(ctx, b.OrgID, id)
}

func (s *ProjectsStore) GetBinding(ctx context.Context, orgID, bindingID string) (projects.Binding, error) {
	b, err := scanBinding(s.db.pool.QueryRow(ctx, bindingSelect+` WHERE org_id = $1 AND id = $2`, orgID, bindingID))
	if isNoRows(err) {
		return b, projects.ErrNotFound
	}
	return b, err
}

func (s *ProjectsStore) ListBindings(ctx context.Context, orgID, userID string) ([]projects.Binding, error) {
	rows, err := s.db.pool.Query(ctx, bindingSelect+` WHERE org_id = $1 AND ($2 = '' OR user_id::text = $2) ORDER BY updated_at DESC`, orgID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []projects.Binding{}
	for rows.Next() {
		b, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// UpdateBinding 用 ETag 做条件更新。ETag 由 updated_at 与 applied_revision 派生，
// 任何一次写入都会改变它。
func (s *ProjectsStore) UpdateBinding(ctx context.Context, b projects.Binding, ifMatch string) (projects.Binding, error) {
	tag, err := s.db.pool.Exec(ctx, `
		UPDATE bindings SET display_name = $3, project_ids = $4::uuid[], updated_at = clock_timestamp()
		WHERE org_id = $1 AND id = $2
		  AND md5(updated_at::text || coalesce(applied_revision, '')) = $5`,
		b.OrgID, b.ID, b.DisplayName, b.ProjectIDs, ifMatch)
	if err != nil {
		return projects.Binding{}, err
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.GetBinding(ctx, b.OrgID, b.ID); err != nil {
			return projects.Binding{}, err
		}
		return projects.Binding{}, projects.ErrETagMismatch
	}
	return s.GetBinding(ctx, b.OrgID, b.ID)
}

func (s *ProjectsStore) SetBindingState(ctx context.Context, orgID, bindingID string, state projects.BindingState) error {
	tag, err := s.db.pool.Exec(ctx, `
		UPDATE bindings SET state = $3::binding_state, updated_at = clock_timestamp()
		WHERE org_id = $1 AND id = $2`, orgID, bindingID, state)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return projects.ErrNotFound
	}
	return nil
}
