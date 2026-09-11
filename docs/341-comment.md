# Draft comment for Tencent/teamai-cli#341

> Post this on the issue once the repository is public. Adjust links to the actual repo URL.

---

Hi, we have been building a Go management backend that follows the model in this issue and the draft
in #481, and we would like to contribute it as `server/`.

**What it is**: a single Go binary (PostgreSQL is the only dependency) with an embedded admin console,
plus a small patch to teamai-cli that adds a `server` repo kind: `teamai init --server <url>` logs in
with a browser code (or an admin's enrollment code), binds the directory to projects and syncs. From
then on pull, push, contribute, remove and usage reports go over HTTP. The member's directory needs
no `.git`, no remote, no credentials; every existing ResourceHandler is reused because the snapshot
is materialized in the team-repo layout. We verified this end to end: a non-git directory receives
skills, rules, agents, env, learnings, culture, claudemd, hooks and MCP; a pushed skill goes through
review in the console and comes back on the next pull; secrets in a contribution are rejected.

**What is implemented**

- Domain model from this issue: Organization → Team → Project; accounts separated from org memberships;
  owner / admin / member / viewer plus team- and project-level roles; permissions derived, not stored.
- Joining without Git (J1–J3): device authorization flow, one-time enrollment codes that pre-bind
  projects, generic OIDC with IdP-group → role mappings, refresh rotation with reuse detection,
  revocation propagated in one transaction.
- Changesets instead of MRs (J4): draft → in_review → approved → published; reviews carry a digest and
  are superseded on edit; org-level `required_approvals`; admin fast track; publish is one transaction
  across projects with base-revision checks, and non-overlapping stale bases are auto-accepted.
- Sync: full snapshot per binding, `revision = sha256(canonical snapshot)`, ETag / 304, blobs by
  sha256 with visibility checks, sync results reported back for distribution audit.
- Eleven resource kinds (skill, rule, doc, agent, hook, mcp, env, claudemd, culture, policy, learning)
  with org / team / project levels, tombstones, and stricter-wins policy merging; permission groups as
  optional bundles that map to `manifest/roles.yaml`.
- Learnings publish directly with server-side secret scanning (`422 SECRET_DETECTED`), confidence
  scoring, archive, and promotion to rule / doc / skill.
- One deduplicated ingestion endpoint for votes / sessions / usage / skill usage / tool use, with prompt
  text and absolute paths having no landing column.
- Weekly digest, KB health, audit, `Idempotency-Key` on writes.

**Docs**: English design docs are in `docs/en/` (identity, resources, sync, changesets, knowledge); the
API table and schema are in the spec.

**Open questions for maintainers**

1. The teamai-cli side is a ~1.8k-line patch (new `server-repo.ts`, `server-format.ts`,
   `server-write.ts`, small branches in init/pull/push/contribute/remove/status). Would you take it as
   a PR against `main`, or prefer it split (transport first, write direction second)?
2. The server renders the flat layout; namespaced `manifest/roles.yaml` / `projects.yaml` are also
   available. Which should be the default for server-backed teams?
3. Licensing: our repo is AGPL-3.0; the contribution to `server/` would be relicensed to MIT to match
   this repository.

Happy to open a PR whichever way you prefer.
