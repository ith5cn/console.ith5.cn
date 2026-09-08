package db

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ith5/ith5/internal/core"
)

// SeedDemo 建立一份可直接跑通端到端流程的演示数据。
// 仅供本地开发使用，绝不在生产调用。
func (d *DB) SeedDemo(ctx context.Context, orgSlug, email, pwHash string) (string, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var orgID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO organizations (name, slug) VALUES ($1, $2)
		ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name
		RETURNING id::text`, "Demo Org", orgSlug).Scan(&orgID); err != nil {
		return "", fmt.Errorf("组织: %w", err)
	}

	var userID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (org_id, email, name, role, password_hash)
		VALUES ($1, $2, 'Demo User', 'owner', $3)
		ON CONFLICT (org_id, email) DO UPDATE SET password_hash = EXCLUDED.password_hash
		RETURNING id::text`, orgID, email, pwHash).Scan(&userID); err != nil {
		return "", fmt.Errorf("用户: %w", err)
	}

	// 三个 bundle：全员一个、后端两个
	type spec struct {
		name, kind, desc string
		files            []core.File
	}
	specs := []spec{
		{"corp-common", "skill", "全员通用规范", []core.File{
			{Path: "SKILL.md", Content: "---\ndescription: 公司通用规范\n---\n\n遵循公司通用编码规范。\n"},
		}},
		{"corp-db-review", "skill", "数据库变更审查", []core.File{
			{Path: "SKILL.md", Content: "---\ndescription: 审查数据库变更\n---\n\n审查 DDL 与索引。\n"},
			{Path: "references/checklist.md", Content: "- 是否有回滚脚本\n- 是否锁表\n"},
		}},
		{"corp-deploy", "command", "发布流程", []core.File{
			{Path: "SKILL.md", Content: "---\ndescription: 发布流程\ndisable-model-invocation: true\n---\n\n执行发布检查单。\n"},
		}},
	}

	bundleIDs := map[string]string{}
	for _, sp := range specs {
		if err := core.ValidateName(sp.name); err != nil {
			return "", err
		}
		if err := core.ValidateFiles(core.Kind(sp.kind), sp.files); err != nil {
			return "", fmt.Errorf("%s: %w", sp.name, err)
		}
		var bid string
		if err := tx.QueryRow(ctx, `
			INSERT INTO bundles (org_id, name, kind, scope, description)
			VALUES ($1,$2,$3::bundle_kind,'enterprise',$4)
			ON CONFLICT (org_id, kind, name) DO UPDATE SET description = EXCLUDED.description
			RETURNING id::text`, orgID, sp.name, sp.kind, sp.desc).Scan(&bid); err != nil {
			return "", fmt.Errorf("bundle %s: %w", sp.name, err)
		}
		bundleIDs[sp.name] = bid

		// checksum 由 core 计算 —— 与客户端落盘前的校验用的是同一份实现
		sum, err := core.Checksum(sp.files)
		if err != nil {
			return "", err
		}
		blob, err := json.Marshal(core.CanonicalFiles(sp.files))
		if err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO bundle_versions (bundle_id, version, files, checksum, changelog, published_by)
			VALUES ($1, 1, $2, $3, '初始版本', $4)
			ON CONFLICT (bundle_id, version) DO NOTHING`, bid, blob, sum, userID); err != nil {
			return "", fmt.Errorf("版本 %s: %w", sp.name, err)
		}
	}

	// 权限组：后端工具包 = db-review + deploy
	var groupID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO permission_groups (org_id, key, name, description)
		VALUES ($1,'backend-pack','后端工具包','后端岗位使用的技能集合')
		ON CONFLICT (org_id, key) DO UPDATE SET name = EXCLUDED.name
		RETURNING id::text`, orgID).Scan(&groupID); err != nil {
		return "", fmt.Errorf("权限组: %w", err)
	}
	for _, n := range []string{"corp-db-review", "corp-deploy"} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO permission_group_bundles (group_id, bundle_id) VALUES ($1,$2)
			ON CONFLICT DO NOTHING`, groupID, bundleIDs[n]); err != nil {
			return "", err
		}
	}

	// 授权：corp-common 给全组织；后端工具包给这个用户
	if _, err := tx.Exec(ctx, `
		INSERT INTO assignments (org_id, bundle_id, subject_type) VALUES ($1,$2,'org')
		ON CONFLICT DO NOTHING`, orgID, bundleIDs["corp-common"]); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO assignments (org_id, group_id, subject_type, subject_id)
		VALUES ($1,$2,'user',$3) ON CONFLICT DO NOTHING`, orgID, groupID, userID); err != nil {
		return "", err
	}

	return userID, tx.Commit(ctx)
}
