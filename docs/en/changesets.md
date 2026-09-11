# Design: changesets, review, and publishing

> Status: final (2026-09-10). Translates Git branch / MR / merge from #341 into server-side concepts on
> top of the existing bundle / bundle_versions structure.

## 1. Why

#341 asks for draft → review → publish, where one publish may span several resources and projects and
take effect atomically. That needs a first-class "changeset" object.

## 2. State machine

```
                 submit              approve              publish
  DRAFT ───────────▶ IN_REVIEW ───────────▶ APPROVED ───────────▶ PUBLISHED
    ▲                  │  │                    │
    │  request_changes │  │ reject             │ content or base changed
    └──────────────────┘  ▼                    ▼
                       REJECTED              DRAFT (reviews superseded)

  DRAFT / IN_REVIEW / APPROVED ──cancel──▶ CANCELLED
```

PUBLISHED, REJECTED, and CANCELLED are terminal. To change anything, open a new changeset.

## 3. Data model

```
changesets
  id, org_id, author_id
  state            enum(draft, in_review, approved, published, rejected, cancelled)
  title, description
  base_revisions   jsonb   {scope_key: revision} locked at creation
  submitted_digest text    SHA-256 over all operations, frozen at submit
  fast_track       bool
  release_id       uuid
  etag, created_at, updated_at

changeset_operations
  changeset_id, seq
  op               enum(put, delete)
  level            enum(org, team, project)
  project_id / team_id per level
  kind, name
  files            jsonb   [{path, sha256, size}] referencing verified blobs (put only)
  expected_prev_version int

changeset_reviews
  id, changeset_id, reviewer_id
  decision         enum(approve, request_changes, reject)
  digest           text    the submitted_digest the reviewer saw
  superseded       bool    set when the digest changes
  comment, created_at

releases
  id, org_id, changeset_id, published_by, published_at
  items            jsonb   [{bundle_id, kind, name, version, deleted, scope_key}]
  revisions        jsonb   {scope_key: new_revision}

content_heads
  scope_key PK, revision, release_id, updated_at

blobs
  sha256 PK, org_id, size, bytes, uploaded_by, created_at
```

Rules:

- **Deletion is explicit.** Only `op=delete` deletes; "this file was not included" never means delete.
- The **resource key** is `(level, owner, kind, name)`, not the file name.
- Blob uploads verify the declared sha256 and size.

## 4. Transitions

| Transition | Who | Server checks |
|---|---|---|
| create DRAFT | `resource:write` | base revision exists for every scope; every blob is verified and belongs to the org |
| edit DRAFT | author | requires If-Match; any edit clears reviews and returns APPROVED to DRAFT |
| submit | author | operations non-empty; compute and freeze `submitted_digest` |
| approve / request_changes / reject | `review:decide` and **not the author** | the review's digest must equal the current `submitted_digest` |
| publish | `release:publish` | see §5 |
| cancel | author or org admin | not terminal |

Review invalidation has one rule: **if the digest changed, the review no longer counts**; old reviews are
marked `superseded`. There is no "review expired" state.

## 5. Publish: one transaction

1. Open a transaction; lock `content_heads` for every target scope, in sorted key order.
2. Compare `base_revisions` with the current heads.
   - All equal → continue.
   - Otherwise check whether this changeset's operations touch the same resource keys as any release
     published after the base. **Non-overlapping → auto-accept** on the current head; overlapping →
     reject the whole publish with `412 REVISION_MISMATCH`, listing the conflicting keys; the changeset
     stays APPROVED and the author rebuilds.
3. For each `put`: insert a `bundle_versions` row with `version = max + 1`; create the bundle if needed.
4. For each `delete`: insert a tombstone version (`files=[]`, `deleted=true`) that also consumes a
   version number. Clients uninstall on seeing a tombstone.
5. Insert the `releases` row with every `(bundle_id, version)` and each scope's new revision.
6. Update `content_heads`; write the audit event.
7. Mark the changeset PUBLISHED; commit.

Clients only ever see the old head or the new head, never an intermediate state.

**Revision**: after a release, take the scope's currently effective `(kind, name, version)` triples,
sort them, concatenate, SHA-256. Opaque, comparable for equality only, no signing key.

**Rollback**: a new changeset whose `put` operations reference historical version content, going through
the same review and publish path. History is never rewritten.

## 6. Decisions

| # | Question | Decision |
|---|---|---|
| 1 | Keep direct admin publishing | **yes, as a fast track**: holders of `release:publish` may create a changeset with `fast_track=true`; the server still records a full changeset and release, skipping review; visible in audit. Authors still cannot review their own work |
| 2 | Number of approvals | **org-level `required_approvals`, default 1** |
| 3 | Partial approval | **no**; use request_changes and split into several changesets |
| 4 | Who resolves publish conflicts | **the author rebuilds**; the server auto-accepts only non-overlapping changes |

## 7. Mapping to the teamai CLI

| CLI behaviour | Server |
|---|---|
| `teamai push` finds N changes | upload N blobs → create DRAFT with N puts → submit |
| push "updates the existing MR" | the author already has an IN_REVIEW changeset with the same resource → edit it instead of creating |
| `teamai remove` | a changeset containing only deletes |
| MR link | the changeset detail page |
| MR merge | publish |
| admin edits and publishes in the console | fast-track changeset |

## 8. Out of scope

- Partial approval, server-side three-way merge, cross-organization atomic publish.
- Signed manifests: the revision is derived from content hashes; no signing key for now.
