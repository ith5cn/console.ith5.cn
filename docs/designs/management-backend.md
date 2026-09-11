# TeamAI Management Backend (Go) — Design

> Companion to Tencent/teamai-cli#341. The Chinese version is
> [management-backend.zh-CN.md](./management-backend.zh-CN.md); sections, API names, state machines,
> phases and acceptance criteria are kept identical. Detailed per-topic documents live in
> [`docs/en/`](../en/) and the API table in [`docs/开发规格.md`](../开发规格.md).

## 1. Capability mapping and domain model

Every read/write capability of the Git team repo has a backend equivalent:

| Git team repo | Backend |
|---|---|
| clone / pull | `GET /v1/bindings/{id}/snapshot` + `GET /v1/blobs/{sha256}` (full manifest, content by hash, ETag / 304) |
| commit | ChangeSet with `put` / `delete` operations |
| branch | ChangeSet in `draft` |
| MR / PR | ChangeSet in `in_review` with Reviews |
| merge | `POST /v1/change-sets/{id}/publish` (one transaction, cross-project) |
| Git revision | `revision = sha256(canonical snapshot)` per scope and per binding |
| Git conflict | `412 REVISION_MISMATCH` on publish when a stale base overlaps a later release |
| revert | rollback = a new ChangeSet re-publishing historical content |
| `teamai.yaml`, `skills/`, `rules/`, `docs/`, `env/`, `agents/`, `hooks/`, `mcp/`, `learnings/`, `culture.md`, `claudemd/` | resource kinds skill, rule, doc, env, agent, hook, mcp, learning, culture, claudemd, policy |
| `tags.yaml`, `manifest/roles.yaml`, `manifest/projects.yaml` | generated from resource tags, permission-group grants and bound projects |
| `members/`, `stats/`, `votes/`, `sessions/` | reporting surface: `POST /v1/reports/events` (usage_daily, skill_usage, vote_delta, session_summary, tool_use) |
| `sources` | not supported (non-goal) |

Domain objects: Organization, Team, Project, WorkspaceBinding; Account, User (membership), Role,
Device; Resource (bundle), ResourceVersion, Revision; ChangeSet, Review, Release; Learning, Vote,
UsageEvent, Session, AuditEvent. Two planes: the **published resource plane** (resources, versions,
releases, snapshots) and the **user reporting plane** (votes, sessions, usage, device state).

Rules: most specific level wins (project > team > org > permission group); a tombstone at a lower
level masks the parent; deletions are explicit operations and propagate through the next snapshot;
versions are immutable integers per resource; publishing checks base revisions and rejects overlapping
stale changes; a resource placed in a permission group is shared only through assignments.

## 2. Zero-Git user journeys

- **J1 SSO first login**: console → `GET /v1/auth/oidc/authorize?org=<slug>` → IdP → callback → account
  created, memberships derived from group mappings → session token.
- **J2 non-Git member joins one project**: admin creates an enrollment code scoped to the project →
  member runs `ith5-materialize login --server … --enroll <code>` → device flow approved in the
  browser (`/activate`) → binding created automatically → `sync` → `teamai init --self` + `teamai pull`.
- **J3 several projects**: `bind --project a,b` (or an enrollment code with several projects); the
  snapshot resolves both; same-level conflicts are marked and left untouched on the client;
  `projects` lists what the member can bind; `unbind` leaves a directory.
- **J4 cross-project review and publish**: the member edits `.teamai/` and runs `push`; the server
  creates (or updates) a changeset spanning projects; reviewers approve in the console; publish is one
  transaction; the next `sync` returns 200 with the new revision (or 304 when unchanged).

Also covered: offline retry (sync results are re-sent next time; 304 makes repeated syncs free),
permission failure (404 for invisible scopes, 403 for forbidden actions), device unbind and logout
(`unbind`, `logout --purge`), disabled account (empty manifest → client uninstalls).

## 3. Multi-project model

Organization → Team → Project, plus per-user personalization kept on the client (exclusions, tag
subscriptions, local env overrides). Console supports project create / archive, member roles,
draft / review / publish state, version history, diff preview, rollback, permission groups and
assignments with expiry, device and sync status, audit.

## 4. Identity extension points

`identity.Provider` interface (`local`, generic `oidc`; proprietary providers plug in behind the same
interface). Stable external identity mapping via `accounts(issuer, subject)`; org / team / project role
derivation from IdP groups via `idp_group_mappings` with priorities; first-login provisioning; revocation
propagated in one transaction (refresh family, devices, bindings); reconciliation report and apply for
members whose groups changed since their last login; IdP outage → new logins and writes rejected,
existing access tokens keep reading for 15 minutes. Service accounts and SCIM are out of scope for
this phase.

## 5. API contract and state machines

Versioned prefix `/v1`; JWT bearer (login / session / device kinds); `ETag` + `If-Match` (mandatory on
changesets and bindings, optional elsewhere); `Idempotency-Key` on writes (24 h replay, 409 on
mismatch); cursor pagination on every list; stable error codes; `X-Client-Version` against
`min_client_version` in `/v1/capabilities`; blob integrity by sha256.

ChangeSet states: `draft → in_review → approved → published`; `rejected` and `cancelled` terminal;
any edit supersedes reviews and returns to `draft`. Review decisions carry the submitted digest.

## 6. Go monorepo blueprint

```
cmd/ith5-server, cmd/ith5-materialize
internal/{identity, organizations, projects, resources, changesets, releases, sync, knowledge,
          telemetry, audit, maintenance, idempotency, teamaifmt, db, api, platform/{config,httpx,pg,ratelimit,metrics}}
db/migrations, web/
```

Infrastructure is behind interfaces per module (stores, blob access through the resources store,
transactions via `pg.WithTx`, identity providers, audit sink). One binary, PostgreSQL only.

## 7. Security and operations

Tenant isolation by `org_id` on every query; project-level RBAC derived from roles; token kinds with
different scopes; env / secret separation (`secret: true` variables carry no value on the server);
secret scanning on every text file of every resource kind; sha256 integrity for blobs; no remote
command execution; audit on every management action; rate limiting; idempotency; retention policy
with a daily janitor; `/metrics` (Prometheus text) and `/healthz`; backup and restore documented
in `docs/运维-备份与恢复.md`.

## 8. Phased delivery

S0 reset and identity → S1 organizations and devices → S2 resources and sync → S3 changesets and
releases → S4 knowledge and reporting → S5 console → S6 docs and idempotency → S7 client push,
retention, reconciliation, pagination / optimistic locking / client version, secrets, namespaced
layout, metrics. All stages are complete and recorded with evidence in the spec.

## 9. Acceptance, open decisions, non-goals

Acceptance: every read/write path of `pull.ts`, `push.ts`, `local-agent.ts`, `team-push.ts` and
`src/resources/` has a backend mapping (table above); the four journeys run end to end with the
unmodified CLI; identity boundary covers OIDC, pluggable providers, group sync, device flow,
revocation and IdP outage; both language versions of this document match.

Open decisions for maintainers: whether the materialize step should become an HTTP adapter inside
teamai-cli; whether the flat or the namespaced layout is the preferred default; relicensing of the
contributed `server/` directory.

Non-goals: `sources`, `packages`, incremental sync cursors, signed manifests, partial approval,
server-side three-way merge, cross-organization atomic publish, SCIM, service accounts.
