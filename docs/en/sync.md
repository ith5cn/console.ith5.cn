# Design: revisions and the sync protocol

> Status: final (2026-09-11). Aligned with the #341 / PR #481 snapshot semantics; no incremental cursor.

## 1. Core idea

**Always send the full manifest, fetch content by file hash, and let the revision be the hash of the
manifest.**

- A manifest is small (a few hundred entries, tens of KB); incremental sync is not worth it.
- Content is fetched by sha256; anything already on disk is never downloaded again. Cross-version
  dedup and zero-download rollback follow from this.
- The revision is derived from the manifest, so any change produces a new revision without separate
  head counters or grant version numbers.
- No signed manifest, no signing keys.

## 2. Objects

| Object | Meaning |
|---|---|
| Device | one machine, created by the device authorization flow, revocable (`machines.revoked_at`) |
| Binding | one working directory on a device bound to several projects; stores only a path hash and display name |
| Snapshot | the full manifest resolved for a binding |
| Revision | SHA-256 of the canonicalized snapshot |

```
bindings
  id, org_id, user_id, device_id
  workspace_id     text    path hash computed by the client
  display_name     text
  project_ids      uuid[]
  applied_revision text    last revision the client reported as applied
  state            enum(active, suspended, revoked)
  created_at, updated_at, last_sync_at
```

## 3. Endpoints

```
POST /v1/bindings
  {workspace_id, display_name, project_ids}
  → validates membership of every project; returns the binding
  PATCH /v1/bindings/{id} requires If-Match

GET  /v1/bindings/{id}/snapshot
  If-None-Match: "<revision>"
  304  unchanged
  200  see §4

GET  /v1/blobs/{sha256}
  raw bytes. Allowed only when the sha256 is referenced by a resource version currently visible to
  this user; otherwise 404 (no distinction between missing and forbidden).

POST /v1/bindings/{id}/sync-results
  {applied_revision, results: [{kind, name, action, detail}]}
  action ∈ installed | updated | removed | conflict_skipped | failed
  → written to distribution_logs; updates bindings.applied_revision
```

**Deletions are not sent separately: anything absent from the manifest is to be removed.** This only
holds because the manifest is always complete.

## 4. Snapshot shape

```json
{
  "revision": "sha256:…",
  "generated_at": "2026-09-11T08:00:00Z",
  "projects": [{"id": "…", "slug": "billing", "revision": "sha256:…"}],
  "policy": {"rules": {"enforced": ["security-baseline"]}, "hooks": {"autoApply": true}},
  "grants": [{"group_key": "backend-pack", "name": "Backend pack"}],
  "resources": [
    {"kind": "skill", "name": "deploy-helper", "namespace": "billing", "level": "project",
     "version": 7, "version_id": "…", "sha256": "…", "size": 12345, "tags": ["deploy"],
     "files": [{"path": "SKILL.md", "sha256": "…", "size": 2048}], "status": "ok"},
    {"kind": "rule", "name": "naming", "status": "conflict",
     "conflict": {"projects": ["billing", "payments"], "versions": [3, 5]}}
  ],
  "limits": {"max_blob_bytes": 33554432, "max_snapshot_bytes": 1073741824, "max_entries": 5000}
}
```

- `namespace`: project slug or group key; org level is `common`. The client uses it for
  `skills/<ns>/` and `claudemd/<ns>/`.
- `grants` + `projects` produce `manifest/roles.yaml` and `manifest/projects.yaml`.
- The resource-level `sha256` hashes the canonicalized file list, for a quick whole-entry comparison.
- `status=conflict` entries carry no `files`; the client keeps the version from its previous snapshot.

## 5. Server-side computation

1. Check that the device, the binding, and the user are all active. A disabled user gets an **empty
   `resources` list** (the client uninstalls everything). A revoked device gets 401.
2. Resolve by level (see resources.md §4): org → team → project + groups; most specific wins; same-level
   conflicts are marked.
3. Merge `policy` field by field.
4. Canonicalize: sort resources by (kind, name), fixed field order, no whitespace; hash everything except
   `generated_at` and `limits` as the `revision`.
5. Compare with `If-None-Match`; equal means 304.

## 6. Client-side rules (for a future teamai adapter)

- Download into a staging area, verify each file's sha256, then promote to an immutable
  `<revision>/` cache directory.
- Keep an apply journal while materializing into tool directories: target path, expected old hash,
  new hash, state. Recover by hash after a crash.
- If the target file's hash is neither the old nor the new value, the user edited it:
  `conflict_skipped`, never overwrite.
- Report `applied_revision` only after everything is materialized; a failed report does not affect local
  state and is retried next time.
- 401 means the device was revoked: stop syncing, keep local files, tell the user. Remote wipe is not
  promised.
- Read limits from `limits`; do not hard-code them.

## 7. Decisions

| # | Question | Decision |
|---|---|---|
| 1 | Fetch by file hash or zip per resource | **by file hash**; an adapter that wants zips can have the server build them |
| 2 | Authenticated direct download vs. signed URLs + object storage | **direct download**; content stays in PostgreSQL |
| 3 | Incremental `changes?cursor=` | **not in phase one**; the endpoint slot is reserved |

## 8. Limits (initial values, delivered in `limits`)

| Item | Value |
|---|---|
| single blob | 32 MiB |
| single snapshot, uncompressed | 1 GiB |
| manifest entries | 5000 |
| file paths | relative only; no `..`, absolute paths, symlinks, drive letters, or UNC |
