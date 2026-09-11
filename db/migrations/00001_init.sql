-- +goose Up
-- +goose StatementBegin

-- ============================================================
-- 身份（docs/设计-身份与组织模型.md §1）
-- ============================================================

-- 账号是全局身份，键是 (issuer, subject)。邮箱只做展示与联系。
CREATE TABLE accounts (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    issuer      text NOT NULL,             -- 'local' 或 OIDC issuer URL
    subject     text NOT NULL,             -- local: 邮箱；OIDC: sub claim
    email       text NOT NULL,
    name        text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    disabled_at timestamptz,
    UNIQUE (issuer, subject)
);
CREATE INDEX accounts_email_idx ON accounts (lower(email));

CREATE TABLE organizations (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    slug        text NOT NULL UNIQUE,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TYPE org_role    AS ENUM ('owner', 'admin', 'member', 'viewer');
CREATE TYPE user_status AS ENUM ('active', 'suspended');

-- users 是「某账号在某组织的成员记录」。一个账号可以有多行，每组织一行。
CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    account_id    uuid NOT NULL REFERENCES accounts(id)      ON DELETE CASCADE,
    email         text NOT NULL,
    name          text NOT NULL DEFAULT '',
    role          org_role    NOT NULL DEFAULT 'member',
    status        user_status NOT NULL DEFAULT 'active',
    -- 仅 local 账号有值；OIDC 账号为 NULL
    password_hash text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, account_id),
    UNIQUE (org_id, email)
);

-- 组织级策略：审核人数、learnings 是否审核、置信度阈值、保留期。
CREATE TABLE org_policies (
    org_id              uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    required_approvals  int     NOT NULL DEFAULT 1 CHECK (required_approvals >= 1),
    learnings_review    boolean NOT NULL DEFAULT false,
    confidence_prune    numeric(4,3) NOT NULL DEFAULT 0.150,
    confidence_promote  numeric(4,3) NOT NULL DEFAULT 0.700,
    retention_months    int     NOT NULL DEFAULT 13,
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE idp_providers (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id            uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind              text NOT NULL CHECK (kind IN ('oidc')),
    issuer            text NOT NULL,
    client_id         text NOT NULL,
    client_secret_enc bytea,
    scopes            text[] NOT NULL DEFAULT '{openid,profile,email}',
    enabled           boolean NOT NULL DEFAULT true,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, issuer)
);

-- 资源与授权的落点层级。三处共用：bundles、reviewers、idp_group_mappings。
CREATE TYPE owner_level AS ENUM ('org', 'team', 'project');

CREATE TABLE idp_group_mappings (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    provider_id uuid NOT NULL REFERENCES idp_providers(id) ON DELETE CASCADE,
    idp_group   text NOT NULL,
    target      owner_level NOT NULL,
    target_id   uuid,                       -- org 时为 NULL
    role        text NOT NULL,              -- org: owner/admin/member；team/project: admin/member
    priority    int  NOT NULL DEFAULT 0,
    UNIQUE (provider_id, idp_group, target, target_id)
);

-- ============================================================
-- 组织结构（§2）
-- ============================================================

CREATE TYPE member_role AS ENUM ('admin', 'member');

CREATE TABLE teams (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name       text NOT NULL,
    slug       text NOT NULL,
    archived   boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);

CREATE TABLE team_members (
    team_id    uuid NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       member_role NOT NULL DEFAULT 'member',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, user_id)
);

CREATE TABLE projects (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    team_id    uuid REFERENCES teams(id) ON DELETE SET NULL,
    name       text NOT NULL,
    slug       text NOT NULL,
    archived   boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);

CREATE TABLE project_members (
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    role       member_role NOT NULL DEFAULT 'member',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id)
);

-- 额外指定的 reviewer。admin 天然是 reviewer，不在此表。
CREATE TABLE reviewers (
    id       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id   uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    level    owner_level NOT NULL CHECK (level IN ('team', 'project')),
    owner_id uuid NOT NULL,
    user_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    UNIQUE (level, owner_id, user_id)
);

-- ============================================================
-- 设备、凭据与绑定（§4；docs/设计-同步协议.md §2）
-- ============================================================

CREATE TABLE machines (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    hostname     text NOT NULL DEFAULT '',
    os           text NOT NULL DEFAULT '',
    fingerprint  text NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, fingerprint)
);
CREATE INDEX machines_last_seen_idx ON machines (last_seen_at);

-- 接入码：管理员生成，一次性、限时、限定项目（#341 旅程 J2）。
CREATE TABLE enrollments (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_ids uuid[] NOT NULL DEFAULT '{}',
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    code_hash   text NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    max_uses    int NOT NULL DEFAULT 1 CHECK (max_uses >= 1),
    used_count  int NOT NULL DEFAULT 0,
    revoked_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

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
    enrollment_id    uuid REFERENCES enrollments(id) ON DELETE SET NULL,
    user_id          uuid REFERENCES users(id)    ON DELETE CASCADE,
    machine_id       uuid REFERENCES machines(id) ON DELETE CASCADE,
    expires_at       timestamptz NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX device_codes_expires_idx ON device_codes (expires_at);

-- refresh token 轮换：同一 family 内旧 token 再次使用即视为泄露，整族撤销。
CREATE TABLE refresh_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash text NOT NULL UNIQUE,
    family_id  uuid NOT NULL,
    user_id    uuid NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    machine_id uuid NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX refresh_tokens_user_idx   ON refresh_tokens (user_id) WHERE revoked_at IS NULL;
CREATE INDEX refresh_tokens_family_idx ON refresh_tokens (family_id);

CREATE TYPE binding_state AS ENUM ('active', 'suspended', 'revoked');

-- 绑定：设备上的一个工作目录绑定到若干项目。只存路径哈希，不存绝对路径。
CREATE TABLE bindings (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id          uuid NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    machine_id       uuid NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
    workspace_id     text NOT NULL,
    display_name     text NOT NULL DEFAULT '',
    project_ids      uuid[] NOT NULL DEFAULT '{}',
    applied_revision text,
    state            binding_state NOT NULL DEFAULT 'active',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    last_sync_at     timestamptz,
    UNIQUE (machine_id, workspace_id)
);

-- ============================================================
-- 内容（docs/设计-资源类型与层级.md）
-- ============================================================

-- 内容按 sha256 寻址，按组织隔离；同一字节在不同组织各存一份。
CREATE TABLE blobs (
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    sha256     text NOT NULL,
    bytes      bytea NOT NULL,
    size       int  NOT NULL CHECK (size >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, sha256)
);

CREATE TYPE bundle_kind AS ENUM (
    'skill', 'rule', 'doc', 'agent', 'hook', 'mcp', 'env',
    'claudemd', 'culture', 'policy', 'learning'
);

CREATE TABLE bundles (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    level       owner_level NOT NULL,
    team_id     uuid REFERENCES teams(id)    ON DELETE CASCADE,
    project_id  uuid REFERENCES projects(id) ON DELETE CASCADE,
    -- 落点 id：org 级用 org_id，便于用一个键做唯一约束与 head 查找
    owner_id    uuid GENERATED ALWAYS AS (COALESCE(team_id, project_id, org_id)) STORED,
    kind        bundle_kind NOT NULL,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    tags        text[] NOT NULL DEFAULT '{}',
    archived    boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, level, owner_id, kind, name),
    CONSTRAINT bundles_level_ck CHECK (
        (level = 'org'     AND team_id IS NULL     AND project_id IS NULL) OR
        (level = 'team'    AND team_id IS NOT NULL AND project_id IS NULL) OR
        (level = 'project' AND team_id IS NULL     AND project_id IS NOT NULL)
    )
);
CREATE INDEX bundles_owner_idx ON bundles (org_id, level, owner_id);

CREATE TABLE bundle_versions (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    bundle_id           uuid NOT NULL REFERENCES bundles(id) ON DELETE CASCADE,
    -- 单调递增整数。回滚 = 用旧内容发布一个新版本，客户端永不处理降级语义。
    version             int  NOT NULL CHECK (version >= 1),
    -- [{path, sha256, size}]，内容在 blobs
    files               jsonb NOT NULL DEFAULT '[]'::jsonb,
    checksum            text  NOT NULL,
    -- tombstone：本版表示删除，客户端据此卸载
    deleted             boolean NOT NULL DEFAULT false,
    changelog           text NOT NULL DEFAULT '',
    rollback_of_version int,
    release_id          uuid,
    published_by        uuid REFERENCES users(id) ON DELETE SET NULL,
    published_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (bundle_id, version)
);

-- 权限组是「内容的集合」，授权给人或项目；与 users.role 正交。
CREATE TABLE permission_groups (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key         text NOT NULL,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    archived    boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, key)
);

CREATE TABLE permission_group_bundles (
    group_id   uuid NOT NULL REFERENCES permission_groups(id) ON DELETE CASCADE,
    bundle_id  uuid NOT NULL REFERENCES bundles(id)           ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, bundle_id)
);
CREATE INDEX permission_group_bundles_bundle_idx ON permission_group_bundles (bundle_id);

CREATE TYPE assignment_subject AS ENUM ('org', 'user', 'project');

CREATE TABLE assignments (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    bundle_id    uuid REFERENCES bundles(id)           ON DELETE CASCADE,
    group_id     uuid REFERENCES permission_groups(id) ON DELETE CASCADE,
    subject_type assignment_subject NOT NULL,
    subject_id   uuid,
    -- 非空表示临时授权，到期自动失效但不删行，保留审计可追溯性
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
CREATE UNIQUE INDEX assignments_bundle_grant_uq
    ON assignments (bundle_id, subject_type, COALESCE(subject_id, '00000000-0000-0000-0000-000000000000'))
    WHERE bundle_id IS NOT NULL;
CREATE UNIQUE INDEX assignments_group_grant_uq
    ON assignments (group_id, subject_type, COALESCE(subject_id, '00000000-0000-0000-0000-000000000000'))
    WHERE group_id IS NOT NULL;

-- ============================================================
-- 变更集、审核、发布（docs/设计-变更集与发布.md）
-- ============================================================

CREATE TYPE changeset_state AS ENUM (
    'draft', 'in_review', 'approved', 'published', 'rejected', 'cancelled'
);

CREATE TABLE changesets (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    author_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    state            changeset_state NOT NULL DEFAULT 'draft',
    title            text NOT NULL,
    description      text NOT NULL DEFAULT '',
    -- {"<level>:<owner_id>": revision}，创建时锁定的基线
    base_revisions   jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- submit 时对全部操作算的 SHA-256；审核记录必须匹配它才生效
    submitted_digest text,
    fast_track       boolean NOT NULL DEFAULT false,
    release_id       uuid,
    -- 每次写入轮换，作为 ETag
    etag             uuid NOT NULL DEFAULT gen_random_uuid(),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX changesets_org_state_idx ON changesets (org_id, state, updated_at DESC);

CREATE TYPE changeset_op AS ENUM ('put', 'delete');

CREATE TABLE changeset_operations (
    changeset_id          uuid NOT NULL REFERENCES changesets(id) ON DELETE CASCADE,
    seq                   int  NOT NULL,
    op                    changeset_op NOT NULL,
    level                 owner_level NOT NULL,
    team_id               uuid REFERENCES teams(id)    ON DELETE CASCADE,
    project_id            uuid REFERENCES projects(id) ON DELETE CASCADE,
    kind                  bundle_kind NOT NULL,
    name                  text NOT NULL,
    -- put 时必填：[{path, sha256, size}]，内容已在 blobs
    files                 jsonb,
    expected_prev_version int,
    PRIMARY KEY (changeset_id, seq),
    CONSTRAINT changeset_operations_files_ck CHECK (
        (op = 'put' AND files IS NOT NULL) OR (op = 'delete' AND files IS NULL)
    )
);

CREATE TYPE review_decision AS ENUM ('approve', 'request_changes', 'reject');

CREATE TABLE changeset_reviews (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    changeset_id uuid NOT NULL REFERENCES changesets(id) ON DELETE CASCADE,
    reviewer_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    decision     review_decision NOT NULL,
    digest       text NOT NULL,
    superseded   boolean NOT NULL DEFAULT false,
    comment      text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX changeset_reviews_cs_idx ON changeset_reviews (changeset_id);

CREATE TABLE releases (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    changeset_id uuid NOT NULL REFERENCES changesets(id),
    published_by uuid REFERENCES users(id) ON DELETE SET NULL,
    published_at timestamptz NOT NULL DEFAULT now(),
    -- [{bundle_id, version}]
    items        jsonb NOT NULL DEFAULT '[]'::jsonb,
    -- {"<level>:<owner_id>": new_revision}
    revisions    jsonb NOT NULL DEFAULT '{}'::jsonb
);

ALTER TABLE changesets      ADD FOREIGN KEY (release_id) REFERENCES releases(id);
ALTER TABLE bundle_versions ADD FOREIGN KEY (release_id) REFERENCES releases(id);

-- 每个落点当前生效的 revision。发布事务按 (level, owner_id) 排序加锁。
CREATE TABLE content_heads (
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    level      owner_level NOT NULL,
    owner_id   uuid NOT NULL,
    revision   text NOT NULL,
    release_id uuid REFERENCES releases(id),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (level, owner_id)
);

-- ============================================================
-- 上报面（docs/设计-知识与上报面.md §3）
-- ============================================================

-- 去重账本：(device, event_id) 唯一，seq 为每设备单调序号
CREATE TABLE report_events (
    machine_id  uuid NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
    event_id    text NOT NULL,
    seq         bigint NOT NULL,
    type        text NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (machine_id, event_id)
);
CREATE INDEX report_events_received_idx ON report_events (received_at);

CREATE TABLE knowledge_votes (
    org_id           uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id          uuid NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    bundle_id        uuid NOT NULL REFERENCES bundles(id) ON DELETE CASCADE,
    recalled_count   int NOT NULL DEFAULT 0,
    upvoted_count    int NOT NULL DEFAULT 0,
    last_recalled_at timestamptz,
    last_upvoted_at  timestamptz,
    PRIMARY KEY (user_id, bundle_id)
);
CREATE INDEX knowledge_votes_bundle_idx ON knowledge_votes (bundle_id);

-- 会话摘要：只有计数与工具名，不含任何提示词或输出文本（L0 禁运清单）
CREATE TABLE sessions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id       uuid NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    machine_id    uuid NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
    session_id    text NOT NULL,
    tool          text NOT NULL DEFAULT '',
    started_at    timestamptz,
    duration_ms   bigint NOT NULL DEFAULT 0,
    prompt_turns  int NOT NULL DEFAULT 0,
    tool_total    int NOT NULL DEFAULT 0,
    tool_sequence text[] NOT NULL DEFAULT '{}',
    interventions jsonb NOT NULL DEFAULT '{}'::jsonb,
    tokens        jsonb NOT NULL DEFAULT '{}'::jsonb,
    valuable      boolean NOT NULL DEFAULT false,
    received_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, session_id)
);
CREATE INDEX sessions_org_time_idx ON sessions (org_id, received_at DESC);

CREATE TABLE usage_daily (
    org_id                      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id                     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day                         date NOT NULL,
    sessions_ended              int    NOT NULL DEFAULT 0,
    sessions_succeeded          int    NOT NULL DEFAULT 0,
    prompt_turns                int    NOT NULL DEFAULT 0,
    duration_ms                 bigint NOT NULL DEFAULT 0,
    sessions_corrected          int    NOT NULL DEFAULT 0,
    priced_requests             int    NOT NULL DEFAULT 0,
    cost_micros                 bigint NOT NULL DEFAULT 0,
    cache_read_tokens           bigint NOT NULL DEFAULT 0,
    cache_eligible_input_tokens bigint NOT NULL DEFAULT 0,
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, day)
);
CREATE INDEX usage_daily_org_day_idx ON usage_daily (org_id, day DESC);

CREATE TABLE skill_usage (
    org_id       uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    skill        text NOT NULL,
    count        int  NOT NULL DEFAULT 0,
    last_used_at timestamptz,
    PRIMARY KEY (user_id, skill)
);

CREATE TYPE distribution_action AS ENUM ('installed', 'updated', 'removed', 'conflict_skipped', 'failed');

-- 同步回执：某台机器对某资源做了什么
CREATE TABLE distribution_logs (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    machine_id  uuid NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
    binding_id  uuid REFERENCES bindings(id) ON DELETE SET NULL,
    kind        bundle_kind NOT NULL,
    name        text NOT NULL,
    version     int,
    action      distribution_action NOT NULL,
    detail      jsonb,
    revision    text,
    ip          inet,
    occurred_at timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX distribution_logs_org_time_idx ON distribution_logs (org_id, created_at DESC);

CREATE TYPE execution_event_type AS ENUM ('session_start', 'tool_use');

-- 工具调用审计：严格白名单字段，服务端重建 summary
CREATE TABLE execution_events (
    id          uuid PRIMARY KEY,          -- 客户端生成，天然幂等
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    machine_id  uuid NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
    session_id  text NOT NULL,
    event_type  execution_event_type NOT NULL,
    tool_name   text,
    summary     jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX execution_events_org_time_idx ON execution_events (org_id, occurred_at DESC);

-- ============================================================
-- 平台（docs/开发规格.md §1.1）
-- ============================================================

-- 管理动作审计：谁在何时对什么做了什么
CREATE TABLE audit_events (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    type          text NOT NULL,
    target_type   text NOT NULL,
    target_id     text NOT NULL DEFAULT '',
    detail        jsonb NOT NULL DEFAULT '{}'::jsonb,
    request_id    text NOT NULL DEFAULT '',
    occurred_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_events_org_time_idx ON audit_events (org_id, occurred_at DESC);

CREATE TABLE idempotency_keys (
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key         text NOT NULL,
    method      text NOT NULL,
    route       text NOT NULL,
    body_sha256 text NOT NULL,
    status      int,
    response    jsonb,
    expires_at  timestamptz NOT NULL,
    PRIMARY KEY (org_id, user_id, key)
);
CREATE INDEX idempotency_keys_expires_idx ON idempotency_keys (expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS
    idempotency_keys, audit_events, execution_events, distribution_logs, skill_usage,
    usage_daily, sessions, knowledge_votes, report_events, content_heads, releases,
    changeset_reviews, changeset_operations, changesets, assignments,
    permission_group_bundles, permission_groups, bundle_versions, bundles, blobs,
    bindings, refresh_tokens, device_codes, enrollments, machines, reviewers,
    project_members, projects, team_members, teams, idp_group_mappings, idp_providers,
    org_policies, users, organizations, accounts CASCADE;
DROP TYPE IF EXISTS
    execution_event_type, distribution_action, review_decision, changeset_op,
    changeset_state, assignment_subject, bundle_kind, binding_state,
    device_code_status, member_role, owner_level, user_status, org_role CASCADE;
-- +goose StatementEnd
