# Design: knowledge (learnings) and the reporting surface

> Status: final (2026-09-11). Aligned with teamai-cli's learnings / votes / sessions / stats data and the
> #341 principle of separating the "publish surface" from the "report surface".

## 1. What teamai reports

| Data | Content | Natural language? |
|---|---|---|
| learnings | experience documents written with `/teamai-share-learnings`; frontmatter title / author / date / tags, Markdown body | **yes**, intentionally shared |
| votes | recall count, upvote count, timestamps per learning; synced incrementally | no |
| sessions | per session: tool sequence, turns, interventions, duration, valuable flag; first prompt line optional | no by default |
| stats | per person per day: sessions, successes, corrections, tokens, estimated cost; skill usage counts | no |
| members | roster | no |

## 2. Learnings go through the publish surface

A learning is content: it needs versions, project isolation, archiving, promotion, and delivery to
project members through the snapshot for recall. So **learning is a resource kind** and uses changesets.

| Item | Rule |
|---|---|
| kind | `learning`, single markdown file, name `<title-slug>-<date>-<random>` (teamai's convention) |
| level | org (shared) or project (private), matching `learnings/` and `learnings/<project-id>/` |
| permission | its own `learning:contribute`, which ordinary members have without `resource:write` |
| publish | **direct**: `teamai contribute` = a changeset with one `put learning`, fast-tracked immediately, fully audited. Org policy `learnings_review` can require review instead |
| edit / archive | authors may edit and archive their own; admins may archive any. Archive = publish a tombstone |
| secret scan | the server scans the body for secret patterns (tokens, private keys, connection strings, Bearer, `key=value`). **A hit is rejected** with the location and type, for the author to fix. No automatic masking |
| supersedes | `contribute` may carry `supersedes[]`; the same changeset publishes tombstones for those |
| recall | happens on the client; the server only delivers. Console search uses PostgreSQL full-text search (with a substring fallback for CJK); no BM25, no code graph |
| promote | "Promote" in the console creates a changeset that `put`s the learning's body as a skill / rule / doc, going through normal review |

## 3. Reporting surface: counting data

One endpoint:

```
POST /v1/reports/events
  {events: [{event_id, seq, type, occurred_at, payload}]}
  type ∈ vote_delta | session_summary | usage_daily | skill_usage | tool_use
  → deduplicated by (device, event_id); seq is a per-device monotonic sequence
  ← {accepted: [event_id…], max_seq}
```

| type | table | rule |
|---|---|---|
| vote_delta | `knowledge_votes` | accumulate; the same event_id never adds twice |
| session_summary | `sessions` | see §4; later events for the same session_id replace rather than add |
| usage_daily | `usage_daily` | upsert by (user, day) |
| skill_usage | `skill_usage` | accumulate |
| tool_use | `execution_events` | the existing audit stream |

The dedup ledger `report_events` is kept for the longest offline window (30 days); older event_ids get
`410 EVENT_WINDOW_EXPIRED` and the client must not resend under a new id.

## 4. Privacy rules (L0 embargo, enforced server-side)

These fields are dropped before storage regardless of whether the client sent them:

- the first prompt line of a session summary (`promptSummary` or any prompt text)
- `stoppedOutput` (AI output fragments)
- `transcriptPath` and any local absolute path
- file contents, diffs, full command lines

What remains: tool names, counts, durations, tokens, timestamps. The implementation parses events into a
whitelist struct, so embargoed fields have no landing column at all.

Learnings are intentionally written and shared and are not embargoed, but are subject to the secret
scan in §2.

## 5. Confidence

Computed from votes, used for maintenance and the health page:

```
confidence = clamp( (upvoted + 1) / (recalled + 2) * age_decay, 0, 1 )
age_decay  = 0.5 ^ (months_since_last_recall / 6)
```

- Below the prune threshold (default 0.15) and older than 90 days → "suggested archive"
  (`recall maintenance --prune`).
- Above the promote threshold (default 0.7) with recalled ≥ 5 → "suggested promotion" (`recall promote`).
- Thresholds are org-level policy.

## 6. Console pages

| Page | Source | teamai equivalent |
|---|---|---|
| Knowledge base | learning resources + votes + confidence; browse, search, archive, promote | `recall`, `recall promote`, `recall maintenance` |
| Weekly digest | `usage_daily` + `sessions` aggregated per week, compared with the previous week | `digest` |
| KB health | counts per kind, top recalled, silent entries, contributions per author, recall trend | dashboard → KB Health |
| Member usage | sessions, correction rate, tokens, cost estimate per person | `stats/` |

## 7. Retention

| Data | Kept for |
|---|---|
| sessions, usage_daily | 13 months (org policy `retention_months`) |
| knowledge_votes | indefinitely |
| report_events ledger | 30 days |
| tool_use | existing policy |

## 8. Decisions

| # | Question | Decision |
|---|---|---|
| 1 | Do learnings need review | **publish directly** (fast track), audited; org policy may require review |
| 2 | First prompt line in session summaries | **always dropped** server-side, even when the client opted in |
| 3 | Learning containing a probable secret | **reject and explain**; no automatic masking |
| 4 | Console search | **PostgreSQL full-text search**; no BM25, no code graph |
