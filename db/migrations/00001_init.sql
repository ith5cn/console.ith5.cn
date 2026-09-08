-- +goose Up
-- +goose StatementBegin

-- ============================================================
-- 组织与人（技术方案 §5.1）
-- ============================================================

CREATE TABLE organizations (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    slug        text NOT NULL UNIQUE,
    -- 「长期无 sync」告警阈值（ADR-002 的补偿手段，技术方案 §17.3）
    stale_threshold_days int NOT NULL DEFAULT 14,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TYPE user_role   AS ENUM ('owner', 'admin', 'member');
CREATE TYPE user_status AS ENUM ('active', 'suspended');

CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email         text NOT NULL,
    name          text NOT NULL DEFAULT '',
    role          user_role   NOT NULL DEFAULT 'member',
    status        user_status NOT NULL DEFAULT 'active',
    password_hash text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, email)
);

CREATE TABLE machines (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    hostname     text NOT NULL DEFAULT '',
    os           text NOT NULL DEFAULT '',
    fingerprint  text NOT NULL,
    -- 每次任一 CLI 写接口调用时更新，供 stale-machines 扫描（技术方案 §17.3）
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, fingerprint)
);
CREATE INDEX machines_last_seen_idx ON machines (last_seen_at);

CREATE TABLE projects (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name       text NOT NULL,
    slug       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);

CREATE TABLE project_members (
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id)
);

-- ============================================================
-- 可分发内容（技术方案 §5.2）
-- ============================================================

-- kind 决定**落盘形态**（PRD C34）：
--   command / skill -> skills/<name>/SKILL.md（目录形态）
--   agent           -> agents/<name>.md      （文件形态）
-- 同一形态内部，kind 只决定生成的 frontmatter 与后台展示（PRD C15/C16）。
CREATE TYPE bundle_kind  AS ENUM ('command', 'skill', 'agent');
-- scope 是授权范围，不是安装位置（PRD §9.4）。
CREATE TYPE bundle_scope AS ENUM ('enterprise', 'project');

CREATE TABLE bundles (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name        text NOT NULL,
    kind        bundle_kind  NOT NULL,
    scope       bundle_scope NOT NULL,
    project_id  uuid REFERENCES projects(id) ON DELETE RESTRICT,
    description text NOT NULL DEFAULT '',
    archived    boolean NOT NULL DEFAULT false,
    draft_files jsonb   NOT NULL DEFAULT '[]'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, kind, name),
    -- scope=project 时 project_id 必填；scope=enterprise 时必须为空（技术方案 §5.5）
    CONSTRAINT bundles_scope_project_ck CHECK (
        (scope = 'project'    AND project_id IS NOT NULL) OR
        (scope = 'enterprise' AND project_id IS NULL)
    )
);

CREATE TABLE bundle_versions (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    bundle_id           uuid NOT NULL REFERENCES bundles(id) ON DELETE CASCADE,
    -- 单调递增整数。不用 semver：客户端因此永远不需要处理「降级」语义，
    -- 回滚 = 用旧内容发布一个新版本（技术方案 §7.1）。
    version             int  NOT NULL,
    files               jsonb NOT NULL,
    checksum            text  NOT NULL,
    changelog           text  NOT NULL DEFAULT '',
    -- 非空表示本版是对该版本的回滚，供控制台标注（PRD C7）
    rollback_of_version int,
    published_by        uuid REFERENCES users(id) ON DELETE SET NULL,
    published_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (bundle_id, version),
    CONSTRAINT bundle_versions_version_ck CHECK (version >= 1)
);

-- ============================================================
-- 权限组与授权（技术方案 §5.2 / PRD §4.3）
-- ============================================================

-- 权限组是一组 skill/command 的具名集合（classic RBAC 里的 role）。
-- 管理员维护「后端工具包」这样的组，再把组授权给人。
--
-- 注意与 users.role 的区别：
--   users.role       = owner/admin/member，管的是「能不能进管理后台」
--   permission_group = 管的是「能拿到哪些内容」
-- 两者正交，不共用字段，也不互相影响。
CREATE TABLE permission_groups (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key         text NOT NULL,          -- 'backend-pack'
    name        text NOT NULL,          -- '后端工具包'
    description text NOT NULL DEFAULT '',
    archived    boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, key)
);

-- 多对多：公用内容（如 corp-git-规范）可同时属于多个权限组，
-- 改一处全部生效，不需要复制多份。
CREATE TABLE permission_group_bundles (
    group_id   uuid NOT NULL REFERENCES permission_groups(id) ON DELETE CASCADE,
    bundle_id  uuid NOT NULL REFERENCES bundles(id)           ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, bundle_id)
);
CREATE INDEX permission_group_bundles_bundle_idx ON permission_group_bundles (bundle_id);

-- 授权主体。
-- 刻意不含 'role'：users.role 只有 owner/admin/member，
-- 把它当授权维度既无实际用途，又会被误读成「岗位」——那是权限组的职责。
CREATE TYPE assignment_subject AS ENUM ('org', 'user', 'project');

CREATE TABLE assignments (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- 授权目标二选一：单个 bundle（一次性/临时授权），或一个权限组（常规路径）
    bundle_id    uuid REFERENCES bundles(id)           ON DELETE CASCADE,
    group_id     uuid REFERENCES permission_groups(id) ON DELETE CASCADE,
    subject_type assignment_subject NOT NULL,
    -- org 时为 NULL；user/project 时存 uuid
    subject_id   text,
    -- 非空表示临时授权，到期自动失效但不删行（保留审计可追溯性，PRD C5）
    expires_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT assignments_subject_ck CHECK (
        (subject_type = 'org' AND subject_id IS NULL) OR
        (subject_type <> 'org' AND subject_id IS NOT NULL)
    ),
    CONSTRAINT assignments_target_ck CHECK (
        (bundle_id IS NOT NULL AND group_id IS NULL) OR
        (bundle_id IS NULL AND group_id IS NOT NULL)
    )
);

-- 逻辑唯一：同一 grant 不重复写入。expires_at 不参与唯一键
-- —— 续期是 UPDATE，不是新增行（技术方案 §5.5）。
CREATE UNIQUE INDEX assignments_bundle_grant_uq
    ON assignments (bundle_id, subject_type, COALESCE(subject_id, ''))
    WHERE bundle_id IS NOT NULL;
CREATE UNIQUE INDEX assignments_group_grant_uq
    ON assignments (group_id, subject_type, COALESCE(subject_id, ''))
    WHERE group_id IS NOT NULL;

-- ============================================================
-- 审计（技术方案 §5.2 / §9）
-- ============================================================

-- conflict_skipped：目标已被用户自有文件占用而跳过。
-- 没有它，「从未分配」和「一直装不上」在审计里长得一模一样（PRD C6）。
CREATE TYPE distribution_action AS ENUM ('install', 'update', 'remove', 'conflict_skipped');

CREATE TABLE distribution_logs (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- 客户端生成，用于回执幂等
    event_id   uuid NOT NULL UNIQUE,
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    machine_id uuid NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
    bundle_id  uuid NOT NULL REFERENCES bundles(id)  ON DELETE CASCADE,
    version    int  NOT NULL,
    action     distribution_action NOT NULL,
    -- conflict_skipped 时记录被占用的目标路径（相对 claude_home，不含 home 绝对路径）
    detail     jsonb,
    ip         inet,
    occurred_at timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX distribution_logs_org_time_idx ON distribution_logs (org_id, created_at DESC);
CREATE INDEX distribution_logs_action_idx   ON distribution_logs (org_id, action, created_at DESC);

CREATE TYPE execution_event_type AS ENUM ('session_start', 'tool_use');

CREATE TABLE execution_events (
    id          uuid PRIMARY KEY,          -- 客户端生成的 UUID，天然幂等
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    machine_id  uuid NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
    session_id  text NOT NULL,
    event_type  execution_event_type NOT NULL,
    tool_name   text,
    -- 严格白名单，服务端二次重建（技术方案 §10.4）
    summary     jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX execution_events_org_time_idx ON execution_events (org_id, occurred_at DESC);

-- ============================================================
-- 认证支撑表（技术方案 §6.1）
-- ============================================================

CREATE TYPE device_code_status AS ENUM ('pending', 'approved', 'consumed');

CREATE TABLE device_codes (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- 只存 hash，不存明文
    device_code_hash text NOT NULL UNIQUE,
    user_code        text NOT NULL UNIQUE,
    fingerprint      text NOT NULL,
    hostname         text NOT NULL DEFAULT '',
    os               text NOT NULL DEFAULT '',
    status           device_code_status NOT NULL DEFAULT 'pending',
    user_id          uuid REFERENCES users(id)    ON DELETE CASCADE,
    machine_id       uuid REFERENCES machines(id) ON DELETE CASCADE,
    expires_at       timestamptz NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX device_codes_expires_idx ON device_codes (expires_at);

CREATE TABLE refresh_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash text NOT NULL UNIQUE,
    user_id    uuid NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    machine_id uuid NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX refresh_tokens_user_idx ON refresh_tokens (user_id) WHERE revoked_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS refresh_tokens, device_codes, execution_events, distribution_logs,
                     assignments, permission_group_bundles, permission_groups,
                     bundle_versions, bundles, project_members, projects,
                     machines, users, organizations CASCADE;
DROP TYPE IF EXISTS device_code_status, execution_event_type, distribution_action,
                    assignment_subject, bundle_scope, bundle_kind,
                    user_status, user_role CASCADE;
-- +goose StatementEnd
