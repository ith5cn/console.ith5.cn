# Design: identity, organizations, and permissions

> Status: final (2026-09-11). Aligned with the #341 / PR #481 domain model
> (Organization / Team / Project / User / Role / Membership / Device / WorkspaceBinding).

## 1. Accounts are separate from memberships

One person can belong to several organizations. Identity is keyed by `(issuer, subject)`; the email
address is display and contact information only.

```
accounts   id, issuer, subject, email, name, created_at, disabled_at
           UNIQUE(issuer, subject)
users      org_id, account_id, email, name, role, status, password_hash …
           = "this account's membership in this organization"
```

- Password login: issuer `local`, subject = email.
- OIDC login: issuer = the IdP issuer URL, subject = the `sub` claim.
- Login resolves the account first, lists its `users` rows, then the person picks an organization.
  Tokens carry the org-scoped `user_id`.

## 2. Roles

```
users.role            owner | admin | member | viewer     organization level
team_members.role     admin | member
project_members.role  admin | member
```

No further role kinds. Fine-grained permissions are derived from these three layers and never stored.

## 3. Permission derivation

| Permission | Who has it |
|---|---|
| `project:read` | project members; members of the owning team; org admin / owner |
| `resource:write` (create a changeset) | members of the target level. Ordinary members may propose but must be reviewed; no fast track |
| `review:decide` | admins of the target level plus explicitly listed reviewers; never the author |
| `release:publish` | admins of the target level; holders may fast-track |
| `learning:contribute` | any member (publishes directly, see knowledge.md) |
| `membership:manage` | org admin for the org; team admin for the team; project admin for the project |
| `telemetry:write` | any device that has not been revoked |
| `audit:read` | org admin / owner |
| `organization:manage` | owner |
| `device:manage` | one's own devices; org admins may revoke anyone's |
| viewer | read-only console; every write is rejected in middleware |

"Members of the target level": org level = everyone in the org; team level = team members; project
level = project members. Extra reviewers live in `reviewers (level, owner_id, user_id)`.

## 4. Devices, bindings, and enrollment

The device authorization flow (`device_codes`, `refresh_tokens`) is the core of #341 P1. Three additions:

**Enrollment codes** (journey J2)

```
enrollments   id, org_id, project_ids uuid[], created_by, code_hash, expires_at,
              max_uses DEFAULT 1, used_count DEFAULT 0, revoked_at
device_codes  + enrollment_id
```

An admin generates a one-time, time-limited code scoped to projects. The member starts the device flow
with the code; on success a binding to those projects is created automatically. Expired, exhausted, or
revoked codes cannot create bindings.

**Refresh reuse detection**: `refresh_tokens.family_id`. Using a rotated-out token revokes the whole
family. Access tokens live 15 minutes.

**Revocation propagation** (one transaction): disabling an account or a membership revokes every refresh
token, marks devices `revoked_at`, marks bindings `revoked`. The next snapshot returns an empty manifest
(user disabled) or 401 (device revoked). Local cleanup happens on the client's next sync; remote wipe is
not promised.

## 5. Identity adapters

```go
type Identity struct {
    Issuer, Subject, Email, Name string
    Groups []string
}

type IdentityProvider interface {
    Kind() string                                              // "local" | "oidc"
    BeginBrowser(state string) (redirectURL string, err error) // auth code + PKCE
    CompleteBrowser(code, state string) (Identity, error)
    VerifyPassword(email, password string) (Identity, error)   // local only
}
```

Phase one implements `local` and generic `oidc` (authorization code + PKCE; the CLI uses the device
flow, the browser completes OIDC). Proprietary enterprise protocols and SCIM keep the interface but are
not implemented. Raw IdP tokens stay inside the adapter and are never stored.

**Group to role mapping**

```
idp_group_mappings   org_id, idp_group, target (org | team:<id> | project:<id>), role, priority
```

- First login: verify the issuer, create the account, then create `users` / `team_members` /
  `project_members` rows from the mappings.
- Later logins: recompute roles from the latest `groups` claim.

**When the IdP is down**: new logins, refreshes, and writes are rejected; already issued access tokens
can still read snapshots for up to 15 minutes.

## 6. Service accounts

Not in scope. CI will later use project-scoped service accounts, not enrollment codes.

## 7. Decisions

| # | Question | Decision |
|---|---|---|
| 1 | Split `accounts` from `users` | yes |
| 2 | Can ordinary members create changesets | yes, but they must be reviewed; no fast track |
| 3 | Who reviews | admins automatically; teams and projects may list extra reviewers |
| 4 | Adapter scope | local + generic OIDC; proprietary protocols and SCIM keep the interface only |
