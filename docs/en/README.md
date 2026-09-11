# ITH5 design documents (English)

ITH5 is a self-hosted Go management backend for [teamai-cli](https://github.com/Tencent/teamai-cli),
built against issue [#341](https://github.com/Tencent/teamai-cli/issues/341) and design draft PR #481.
It replaces the Git team repository with a server, so that people who do not know Git can still join a
team, receive resources, and contribute knowledge. teamai-cli itself is used unmodified.

These are English versions of the Chinese design documents in [`docs/`](../). The Chinese files are the
source of truth; when the two disagree, the Chinese version wins and the English one needs a fix.

| Document | Covers |
|---|---|
| [identity.md](./identity.md) | accounts vs. org memberships, roles, permission derivation, devices, enrollment codes, revocation, OIDC |
| [resources.md](./resources.md) | the 11 resource kinds, org / team / project levels, resolution rules, permission groups, the generated teamai layout |
| [sync.md](./sync.md) | bindings, snapshots, revisions, blobs by hash, 304, sync results, client-side rules |
| [changesets.md](./changesets.md) | state machine, data model, transition rules, the publish transaction, rollback, mapping to CLI commands |
| [knowledge.md](./knowledge.md) | learnings as a resource kind, the reporting surface, privacy rules, confidence, retention |

The full API table, database schema, directory layout, and stage-by-stage acceptance record are in
[`docs/开发规格.md`](../开发规格.md) (Chinese). The single document structured the way issue #341
asks for its deliverable is [`docs/designs/management-backend.md`](../designs/management-backend.md)
(with a matching [zh-CN](../designs/management-backend.zh-CN.md) version).

## Summary for #341

- **Domain model**: Organization → Team → Project; `accounts` (identity, keyed by `(issuer, subject)`)
  are separate from `users` (membership in one organization). Roles are org-level
  owner / admin / member / viewer plus team- and project-level admin / member. Eleven permissions
  are derived from those roles; nothing finer is stored.
- **Joining without Git**: device authorization flow (code shown in the terminal, approved in the
  browser), optional one-time enrollment codes that pre-bind a device to projects, generic OIDC with
  IdP group → role mappings.
- **Resources**: skill, rule, doc, agent, hook, mcp, env, claudemd, culture, policy, learning.
  Most specific level wins; tombstones mask parent versions; `policy` merges field by field toward the
  stricter value.
- **Changesets**: draft → in_review → approved → published, with rejected / cancelled as terminal states.
  Reviews carry the submitted digest and are superseded when the content changes. Publishing is one
  transaction across projects with base-revision checks; non-overlapping stale bases are auto-accepted.
- **Sync**: a full snapshot per binding, `revision = sha256(canonical snapshot)`, ETag / 304, blobs
  fetched by sha256 with visibility checks, sync results reported back as distribution logs.
- **Knowledge and reporting**: learnings publish directly (org policy can require review), secrets are
  rejected with `422 SECRET_DETECTED`, votes / sessions / usage / skill usage / tool use share one
  deduplicated ingestion endpoint, prompt text and absolute paths have no landing column.
- **Idempotency**: `Idempotency-Key` on write requests replays the stored response for 24 hours.
