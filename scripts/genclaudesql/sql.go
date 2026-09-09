package main

const header = `-- ============================================================
-- 从本机 ~/.claude 同步到 ITH5 内容库
--
-- 行为：
--   1. 清空目标组织的全部内容（bundles / bundle_versions /
--      permission_groups / assignments），保留 organizations、users、
--      machines、execution_events。
--   2. 写入下方 bundle，每个发布为 version 1。
--   3. 建一个权限组 claude-baseline，收录全部 bundle，并做组织级授权，
--      使该组织内所有成员 ith5 sync 时都能拿到。
--
-- 注意：distribution_logs 对 bundles 有 ON DELETE CASCADE 外键，
--       清空内容会连带删掉「谁装过哪个 bundle」的分发审计记录。
--       工具调用审计 execution_events 不受影响。
--
-- 执行：psql "$ITH5_DATABASE_URL" -f claude-sync.sql
-- ============================================================

\set ON_ERROR_STOP on

-- 目标组织的 slug。不是 demo 的话改这一行。
\set org_slug 'demo'

BEGIN;

CREATE TEMP TABLE _ctx ON COMMIT DROP AS
SELECT o.id AS org_id,
       (SELECT u.id
          FROM users u
         WHERE u.org_id = o.id AND u.role IN ('owner', 'admin')
         ORDER BY (u.role = 'owner') DESC, u.created_at
         LIMIT 1) AS publisher_id
  FROM organizations o
 WHERE o.slug = :'org_slug';

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM _ctx) THEN
    RAISE EXCEPTION '找不到该 slug 对应的组织，请检查 :org_slug';
  END IF;
END $$;

-- ---------- 1. 清空内容 ----------
-- assignments / permission_group_bundles / bundle_versions 都挂了
-- ON DELETE CASCADE，删 bundles 与 permission_groups 即可级联清干净。
DELETE FROM assignments       WHERE org_id = (SELECT org_id FROM _ctx);
DELETE FROM permission_groups WHERE org_id = (SELECT org_id FROM _ctx);
DELETE FROM bundles           WHERE org_id = (SELECT org_id FROM _ctx);

-- ---------- 2. 写入 bundle ----------
CREATE TEMP TABLE _src (
  name        text,
  kind        bundle_kind,
  description text,
  files       jsonb,
  checksum    text
) ON COMMIT DROP;

INSERT INTO _src (name, kind, description, files, checksum) VALUES
`

const footer = `
INSERT INTO bundles (org_id, name, kind, scope, description)
SELECT (SELECT org_id FROM _ctx), s.name, s.kind, 'enterprise', s.description
  FROM _src s;

INSERT INTO bundle_versions (bundle_id, version, files, checksum, changelog, published_by)
SELECT b.id, 1, s.files, s.checksum, '从 ~/.claude 导入', (SELECT publisher_id FROM _ctx)
  FROM _src s
  JOIN bundles b
    ON b.org_id = (SELECT org_id FROM _ctx)
   AND b.name = s.name
   AND b.kind = s.kind;

-- ---------- 3. 权限组 + 组织级授权 ----------
INSERT INTO permission_groups (org_id, key, name, description)
VALUES ((SELECT org_id FROM _ctx), 'claude-baseline', 'Claude Code 基线包',
        '从 ~/.claude 同步的全部 skill、subagent、规范与环境配置。');

INSERT INTO permission_group_bundles (group_id, bundle_id)
SELECT g.id, b.id
  FROM permission_groups g
  CROSS JOIN bundles b
 WHERE g.org_id = (SELECT org_id FROM _ctx)
   AND g.key = 'claude-baseline'
   AND b.org_id = (SELECT org_id FROM _ctx);

INSERT INTO assignments (org_id, group_id, subject_type)
SELECT (SELECT org_id FROM _ctx), g.id, 'org'
  FROM permission_groups g
 WHERE g.org_id = (SELECT org_id FROM _ctx)
   AND g.key = 'claude-baseline';

-- ---------- 核对 ----------
SELECT kind, count(*) AS bundles
  FROM bundles
 WHERE org_id = (SELECT org_id FROM _ctx)
 GROUP BY kind
 ORDER BY kind;

COMMIT;
`
