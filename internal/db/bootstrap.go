package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ith5/ith5/internal/identity"
	"github.com/ith5/ith5/internal/platform/pg"
	"github.com/ith5/ith5/internal/resources"
)

// Bootstrap 承担建库后的两类一次性写入：生产首个 owner、本地演示数据。
type Bootstrap struct{ db *DB }

// Bootstrap 返回初始化工具。
func (d *DB) Bootstrap() *Bootstrap { return &Bootstrap{db: d} }

// InitOwner 创建或更新首个 owner：组织、local 账号、owner 成员记录。幂等。
func (b *Bootstrap) InitOwner(ctx context.Context, orgName, orgSlug, email, name, pwHash string) (string, error) {
	email = identity.NormalizeEmail(email)
	var userID string
	err := pg.WithTx(ctx, b.db.pool, func(tx pgx.Tx) error {
		orgID, err := upsertOrg(ctx, tx, orgName, orgSlug)
		if err != nil {
			return err
		}
		acctID, err := upsertLocalAccount(ctx, tx, email, name)
		if err != nil {
			return err
		}
		userID, err = upsertUser(ctx, tx, orgID, acctID, email, name, "owner", pwHash)
		return err
	})
	return userID, err
}

// SeedDemo 写入一份可直接跑通端到端流程的演示数据。仅供本地开发，绝不在生产调用。
//
// 覆盖：一个组织、owner 与 member 两个账号、一个团队、两个项目、一个权限组。
// 内容类资源在 S2 随资源模块一起补充。
func (b *Bootstrap) SeedDemo(ctx context.Context, orgSlug, ownerEmail, pwHash string) (string, error) {
	ownerEmail = identity.NormalizeEmail(ownerEmail)
	var ownerID, orgID, memberID, groupID string
	projectIDs := map[string]string{}
	err := pg.WithTx(ctx, b.db.pool, func(tx pgx.Tx) error {
		var err error
		orgID, err = upsertOrg(ctx, tx, "Demo Org", orgSlug)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO org_policies (org_id) VALUES ($1) ON CONFLICT (org_id) DO NOTHING`, orgID); err != nil {
			return fmt.Errorf("组织策略: %w", err)
		}

		ownerAcct, err := upsertLocalAccount(ctx, tx, ownerEmail, "Demo Owner")
		if err != nil {
			return err
		}
		ownerID, err = upsertUser(ctx, tx, orgID, ownerAcct, ownerEmail, "Demo Owner", "owner", pwHash)
		if err != nil {
			return err
		}
		memberEmail := "member@example.com"
		memberAcct, err := upsertLocalAccount(ctx, tx, memberEmail, "Demo Member")
		if err != nil {
			return err
		}
		memberID, err = upsertUser(ctx, tx, orgID, memberAcct, memberEmail, "Demo Member", "member", pwHash)
		if err != nil {
			return err
		}

		var teamID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO teams (org_id, name, slug) VALUES ($1, '平台组', 'platform')
			ON CONFLICT (org_id, slug) DO UPDATE SET name = EXCLUDED.name
			RETURNING id::text`, orgID).Scan(&teamID); err != nil {
			return fmt.Errorf("团队: %w", err)
		}
		for _, uid := range []string{ownerID, memberID} {
			role := "member"
			if uid == ownerID {
				role = "admin"
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, $3::member_role)
				ON CONFLICT (team_id, user_id) DO UPDATE SET role = EXCLUDED.role`, teamID, uid, role); err != nil {
				return fmt.Errorf("团队成员: %w", err)
			}
		}

		projects := []struct{ name, slug, team string }{
			{"计费系统", "billing", teamID},
			{"官网", "website", ""},
		}
		for _, p := range projects {
			var pid string
			if err := tx.QueryRow(ctx, `
				INSERT INTO projects (org_id, team_id, name, slug)
				VALUES ($1, NULLIF($2, '')::uuid, $3, $4)
				ON CONFLICT (org_id, slug) DO UPDATE SET name = EXCLUDED.name, team_id = EXCLUDED.team_id
				RETURNING id::text`, orgID, p.team, p.name, p.slug).Scan(&pid); err != nil {
				return fmt.Errorf("项目 %s: %w", p.slug, err)
			}
			projectIDs[p.slug] = pid
			if _, err := tx.Exec(ctx, `
				INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, 'admin')
				ON CONFLICT DO NOTHING`, pid, ownerID); err != nil {
				return err
			}
			if p.slug == "billing" {
				if _, err := tx.Exec(ctx, `
					INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, 'member')
					ON CONFLICT DO NOTHING`, pid, memberID); err != nil {
					return err
				}
			}
		}

		if err := tx.QueryRow(ctx, `
			INSERT INTO permission_groups (org_id, key, name, description)
			VALUES ($1, 'backend-pack', '后端工具包', '后端岗位使用的技能集合')
			ON CONFLICT (org_id, key) DO UPDATE SET name = EXCLUDED.name
			RETURNING id::text`, orgID).Scan(&groupID); err != nil {
			return fmt.Errorf("权限组: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if err := b.seedResources(ctx, orgID, ownerID, memberID, groupID, projectIDs); err != nil {
		return "", err
	}
	return ownerID, nil
}

// seedResources 写入每种 kind 至少一条，覆盖三层落点、权限组授予与 tombstone，
// 让 snapshot 的每条解析规则在演示数据上都能看到效果。
func (b *Bootstrap) seedResources(ctx context.Context, orgID, ownerID, memberID, groupID string, projects map[string]string) error {
	type spec struct {
		level resources.Level
		owner string
		kind  resources.Kind
		name  string
		desc  string
		tags  []string
		files []resources.File
	}
	billing := projects["billing"]
	one := func(kind resources.Kind, content string) []resources.File {
		return []resources.File{{Path: kind.EntryFile(), Content: content}}
	}
	specs := []spec{
		{resources.LevelOrg, orgID, resources.KindSkill, "corp-common", "全员通用规范", []string{"general"}, []resources.File{
			{Path: "SKILL.md", Content: "---\nname: corp-common\ndescription: 公司通用编码规范，写代码前先读\n---\n\n遵循公司通用编码规范：提交前跑测试，PR 描述写清动机。\n"},
			{Path: "references/checklist.md", Content: "- 是否有测试\n- 是否有回滚方案\n"},
		}},
		{resources.LevelOrg, orgID, resources.KindRule, "security-baseline", "安全基线", []string{"security"}, one(resources.KindRule,
			"# 安全基线\n\n- 不在代码里写密钥\n- 外部输入一律校验\n")},
		{resources.LevelOrg, orgID, resources.KindDoc, "arch/overview.md", "架构总览", nil, one(resources.KindDoc,
			"# 架构总览\n\n单体 Go 服务 + PostgreSQL。\n")},
		{resources.LevelOrg, orgID, resources.KindAgent, "code-reviewer", "代码审查 agent", nil, one(resources.KindAgent,
			"name: code-reviewer\ndescription: 审查代码变更，指出正确性与风格问题\nmodel: sonnet\ninstructions: |\n  你是严格的代码审查员。先看正确性，再看可读性。\n")},
		{resources.LevelOrg, orgID, resources.KindHook, "lint-on-edit", "编辑后跑 lint", nil, []resources.File{
			{Path: "HOOK.yaml", Content: "id: lint-on-edit\ndescription: 编辑后自动 lint\nevent: PostToolUse\nmatcher: Edit\ncommand: scripts/lint.sh\ntimeout: 30\n"},
			{Path: "scripts/lint.sh", Content: "#!/bin/sh\necho lint ok\n"},
		}},
		{resources.LevelOrg, orgID, resources.KindMCP, "corp-docs", "公司文档检索", nil, one(resources.KindMCP,
			"name: corp-docs\ndescription: 公司内部文档检索\ntransport: http\nurl: https://mcp.corp.example/docs\n")},
		{resources.LevelOrg, orgID, resources.KindEnv, "CORP_API_BASE", "内部 API 地址", nil, one(resources.KindEnv,
			"value: https://api.corp.example\ndescription: 内部 API 网关\n")},
		{resources.LevelOrg, orgID, resources.KindClaudeMD, "conventions", "通用约定片段", nil, one(resources.KindClaudeMD,
			"## 团队约定\n\n- 中文注释写「为什么」\n- 提交信息用祈使句\n")},
		{resources.LevelOrg, orgID, resources.KindCulture, "culture", "团队文化", nil, one(resources.KindCulture,
			"---\ncompany:\n  name: Demo Org\n  mission: 让每个团队 AI 原生\n---\n\n先跑通，再优化。\n")},
		{resources.LevelOrg, orgID, resources.KindPolicy, "policy", "组织策略", nil, one(resources.KindPolicy,
			"enforced_rules: [security-baseline]\nmcp_allowed_hosts: [\"*.corp.example\"]\nrecall_enabled: false\n")},
		{resources.LevelOrg, orgID, resources.KindLearning, "port-conflict-2026-09-01-a1b2", "端口冲突排查", []string{"troubleshooting"}, one(resources.KindLearning,
			"---\ntitle: 本地 5432 端口冲突排查\nauthor: demo\ndate: 2026-09-01\ntags: [troubleshooting, postgres]\n---\n\n用 55432 避开本机已有的 Postgres。\n")},
		// 项目级：覆盖组织级同名 rule，并有自己的 skill 与 claudemd
		{resources.LevelProject, billing, resources.KindRule, "security-baseline", "计费系统安全基线（更严）", []string{"security"}, one(resources.KindRule,
			"# 计费系统安全基线\n\n- 组织基线全部适用\n- 金额一律用整数分\n")},
		{resources.LevelProject, billing, resources.KindSkill, "billing-deploy", "计费系统发布流程", []string{"deploy"}, one(resources.KindSkill,
			"---\nname: billing-deploy\ndescription: 计费系统的发布检查单与步骤\n---\n\n1. 跑迁移预演\n2. 灰度 10%\n")},
		{resources.LevelProject, billing, resources.KindClaudeMD, "billing-context", "计费上下文", nil, one(resources.KindClaudeMD,
			"## 计费系统\n\n金额单位是分，币种字段必填。\n")},
		{resources.LevelProject, billing, resources.KindPolicy, "policy", "计费策略", nil, one(resources.KindPolicy,
			"enforced_rules: [naming]\nrecall_enabled: true\n")},
		// 权限组内容：只有被授权的人拿到
		{resources.LevelOrg, orgID, resources.KindSkill, "corp-db-review", "数据库变更审查", []string{"database"}, one(resources.KindSkill,
			"---\nname: corp-db-review\ndescription: 审查 DDL 与索引变更\n---\n\n检查是否锁表、是否有回滚脚本。\n")},
	}
	ids := map[string]string{}
	for _, sp := range specs {
		id, err := b.db.PutPublished(ctx, orgID, sp.level, sp.owner, sp.kind, sp.name, sp.desc, sp.tags, sp.files, ownerID)
		if err != nil {
			return fmt.Errorf("seed %s/%s: %w", sp.kind, sp.name, err)
		}
		ids[string(sp.kind)+"/"+sp.name] = id
	}
	// tombstone：组织级 doc 被项目级删除，绑定 billing 的人看不到它
	if err := b.db.PutTombstone(ctx, orgID, resources.LevelProject, billing, resources.KindDoc, "arch/overview.md", ownerID); err != nil {
		return err
	}
	// 权限组 = corp-db-review；授权给 member
	_, err := b.db.pool.Exec(ctx, `
		INSERT INTO permission_group_bundles (group_id, bundle_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		groupID, ids["skill/corp-db-review"])
	if err != nil {
		return err
	}
	_, err = b.db.pool.Exec(ctx, `
		INSERT INTO assignments (org_id, group_id, subject_type, subject_id) VALUES ($1, $2, 'user', $3)
		ON CONFLICT DO NOTHING`, orgID, groupID, memberID)
	return err
}

func upsertOrg(ctx context.Context, tx pgx.Tx, name, slug string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO organizations (name, slug) VALUES ($1, $2)
		ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name
		RETURNING id::text`, name, slug).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("组织: %w", err)
	}
	return id, nil
}

func upsertLocalAccount(ctx context.Context, tx pgx.Tx, email, name string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO accounts (issuer, subject, email, name) VALUES ('local', $1, $1, $2)
		ON CONFLICT (issuer, subject) DO UPDATE SET name = EXCLUDED.name
		RETURNING id::text`, email, name).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("账号: %w", err)
	}
	return id, nil
}

func upsertUser(ctx context.Context, tx pgx.Tx, orgID, accountID, email, name, role, pwHash string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO users (org_id, account_id, email, name, role, status, password_hash)
		VALUES ($1, $2, $3, $4, $5::org_role, 'active', $6)
		ON CONFLICT (org_id, account_id) DO UPDATE SET
			name = EXCLUDED.name, role = EXCLUDED.role, status = 'active',
			password_hash = EXCLUDED.password_hash
		RETURNING id::text`, orgID, accountID, email, name, role, pwHash).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("成员: %w", err)
	}
	return id, nil
}
