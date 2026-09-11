// 与 Go 服务端 /v1 契约对应的类型与调用。
//
// 类型是手写的：服务端是 Go，这里是 TS。契约变更时两边一起改（docs/开发规格.md §1）。

// ---------------------------------------------------------------
// 类型
// ---------------------------------------------------------------

export interface Account { id: string; email: string; name: string }
export interface Membership { user_id: string; org_id: string; org_slug: string; org_name: string; role: string }
export interface Me { account: Account; membership: Membership; machine_id?: string; token_kind: string; permissions: string[] }

export interface Team { id: string; name: string; slug: string; archived: boolean; created_at: string; members?: TeamMember[] }
export interface TeamMember { user_id: string; email: string; name: string; role: string }
export interface Project { id: string; team_id?: string; name: string; slug: string; archived: boolean; created_at: string; members?: TeamMember[] }
export interface Reviewer { user_id: string; email: string }

export interface Member {
  user_id: string; email: string; name: string; role: string; status: string
  machines: number; last_seen_at?: string; created_at: string
}
export interface Device {
  id: string; user_id: string; email: string; hostname: string; os: string
  last_seen_at: string; revoked_at?: string; created_at: string
}
export interface Enrollment {
  id: string; code?: string; project_ids: string[]; created_by?: string; expires_at: string
  max_uses: number; used_count: number; revoked_at?: string; created_at: string; command?: string
}
export interface Binding {
  id: string; machine_id: string; workspace_id: string; display_name: string; project_ids: string[]
  applied_revision?: string; state: string; last_sync_at?: string; updated_at: string; etag: string
}

export interface Policy {
  required_approvals: number; learnings_review: boolean
  confidence_prune: number; confidence_promote: number; retention_months: number
}

export interface FileRef { path: string; sha256: string; size: number }
export interface Resource {
  id: string; level: string; owner_id: string; namespace: string; kind: string; name: string
  description: string; tags: string[]; groups: string[]; archived: boolean
  version: number; version_id: string; checksum: string; deleted: boolean; files: FileRef[]
  published_at?: string; updated_at: string
}
export interface Version {
  id: string; version: number; files: FileRef[]; checksum: string; deleted: boolean
  rollback_of_version?: number; release_id?: string; published_by?: string; published_at: string
  contents?: Record<string, string>
}
export interface ResourceDetail extends Resource { versions: Version[] }

export interface Op {
  seq?: number; op: 'put' | 'delete'; level: string; team_id?: string; project_id?: string
  kind: string; name: string; files?: FileRef[]; expected_prev_version?: number
}
export interface Review { id: string; reviewer_email: string; decision: string; digest: string; superseded: boolean; comment: string; created_at: string }
export interface Changeset {
  id: string; author_email: string; state: string; title: string; description: string
  base_revisions: Record<string, string>; submitted_digest?: string; fast_track: boolean; release_id?: string
  etag: string; created_at: string; updated_at: string; ops: Op[]; reviews: Review[]
}
export interface DiffItem { op: string; kind: string; name: string; scope_key: string; change: string; current_version?: number; files?: FileRef[] }
export interface ReleaseItem { bundle_id: string; kind: string; name: string; version: number; deleted: boolean; scope_key: string }
export interface Release { id: string; changeset_id: string; publisher_email: string; published_at: string; items: ReleaseItem[]; revisions: Record<string, string> }

export interface Group {
  id: string; key: string; name: string; description: string; archived: boolean
  bundle_ids: string[]; bundle_names: string[]; assigned_to: number; created_at: string
}
export interface Assignment {
  id: string; bundle_id?: string; bundle_name?: string; group_id?: string; group_name?: string
  subject_type: string; subject_id?: string; subject_name?: string; expires_at?: string; expired: boolean; created_at: string
}

export interface Learning {
  id: string; level: string; owner_id: string; namespace: string; name: string; title: string; author: string
  tags: string[]; version: number; archived: boolean; published_at: string
  recalled: number; upvoted: number; last_recall?: string; confidence: number; excerpt: string; content?: string
}
export interface DayCount { day: string; count: number }
export interface KBHealth {
  by_kind: Record<string, number>; top_recalled: Learning[]; silent: Learning[]
  prune_candidates: Learning[]; promote_candidates: Learning[]; by_author: Record<string, number>; recall_trend: DayCount[]
}
export interface DigestTotals { sessions: number; sessions_succeeded: number; prompt_turns: number; active_ms: number; cost_micros: number; corrections: number }
export interface Digest extends DigestTotals {
  week_start: string; cache_read_tokens: number; active_members: number; previous?: DigestTotals
  top_skills: { skill: string; count: number }[]
  highlights: { email: string; tool: string; started_at: string; duration_ms: number; tool_total: number; interventions: number }[]
}

export interface AuditEvent { id: string; actor_email?: string; type: string; target_type: string; target_id: string; detail: Record<string, unknown>; request_id?: string; occurred_at: string }
export interface Distribution { email: string; hostname: string; kind: string; name: string; version?: number; action: string; detail?: Record<string, unknown>; revision?: string; occurred_at: string }
export interface Execution { email: string; hostname: string; session_id: string; event_type: string; tool_name?: string; summary: Record<string, unknown>; occurred_at: string }

export interface IdPConfig { id?: string; issuer: string; client_id: string; client_secret?: string; scopes: string[]; enabled: boolean }
export interface GroupMapping { idp_group: string; target: string; target_id?: string; role: string; priority: number }
export interface RoleChange { target: string; target_id?: string; current?: string; expected?: string }
export interface ReconcileDiff { user_id: string; email: string; synced_at: string; changes: RoleChange[] }
export interface MaintenanceReport { ran_at: string; deleted: Record<string, number>; reconcile_diffs: Record<string, number>; errors?: string[] }

interface Page<T> { items: T[]; next_cursor?: string }

// ---------------------------------------------------------------
// 会话状态
// ---------------------------------------------------------------

const TOKEN_KEY = 'ith5_token'
const ME_KEY = 'ith5_me'

export function getToken() { return localStorage.getItem(TOKEN_KEY) }
export function getMe(): Me | null {
  const raw = localStorage.getItem(ME_KEY)
  return raw ? (JSON.parse(raw) as Me) : null
}
export function setSession(token: string, me: Me) {
  localStorage.setItem(TOKEN_KEY, token)
  localStorage.setItem(ME_KEY, JSON.stringify(me))
}
export function clearSession() {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(ME_KEY)
}
export function can(perm: string) { return getMe()?.permissions.includes(perm) ?? false }
export function isViewer() { return getMe()?.membership.role === 'viewer' }

export const DEMO_READ_ONLY_MESSAGE = '当前是只读账号，无法修改'

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string, public details?: unknown) {
    super(message)
  }
}

interface RequestOptions extends RequestInit { ifMatch?: string; raw?: boolean }

async function request<T>(path: string, init: RequestOptions = {}): Promise<T> {
  const method = (init.method ?? 'GET').toUpperCase()
  if (isViewer() && !['GET', 'HEAD'].includes(method)) {
    throw new ApiError(403, 'ACCESS_DENIED', DEMO_READ_ONLY_MESSAGE)
  }
  const token = getToken()
  const headers: Record<string, string> = {
    ...(init.raw ? {} : { 'Content-Type': 'application/json' }),
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...(init.ifMatch ? { 'If-Match': `"${init.ifMatch}"` } : {}),
    ...((init.headers as Record<string, string>) ?? {}),
  }
  const res = await fetch(`/v1${path}`, { ...init, headers })
  if (res.status === 204) return undefined as T
  const body = await res.json().catch(() => ({}))
  if (!res.ok) {
    if (res.status === 401) clearSession()
    const e = body.error ?? {}
    throw new ApiError(res.status, e.code ?? 'ERROR', e.message ?? res.statusText, e.details)
  }
  return body as T
}

const json = (v: unknown) => JSON.stringify(v)

// ---------------------------------------------------------------
// 调用
// ---------------------------------------------------------------

export const api = {
  // ---- 认证 ----
  login: (email: string, password: string) =>
    request<{ account: Account; login_token: string; organizations: Membership[] }>('/auth/login', { method: 'POST', body: json({ email, password }) }),
  async session(login_token: string, user_id: string) {
    const r = await request<{ access_token: string }>('/auth/session', { method: 'POST', body: json({ login_token, user_id }) })
    localStorage.setItem(TOKEN_KEY, r.access_token)
    const me = await request<Me>('/me')
    setSession(r.access_token, me)
    return me
  },
  createOrganization: (login_token: string, name: string, slug: string) =>
    request<{ organization: { id: string; name: string; slug: string }; membership: Membership }>('/organizations', { method: 'POST', body: json({ login_token, name, slug }) }),
  async adoptToken(token: string) {
    localStorage.setItem(TOKEN_KEY, token)
    const me = await request<Me>('/me')
    setSession(token, me)
    return me
  },
  me: () => request<Me>('/me'),
  organizations: () => request<Page<Membership>>('/organizations'),
  devicePeek: (user_code: string) =>
    request<{ hostname: string; os: string; expires_at: string; enrollment?: { id: string; project_ids: string[]; allowed: boolean } }>(`/auth/device/peek?user_code=${encodeURIComponent(user_code)}`),
  deviceActivate: (user_code: string) => request<{ status: string }>('/auth/device/activate', { method: 'POST', body: json({ user_code }) }),

  // ---- 组织 ----
  policy: (orgId: string) => request<Policy>(`/organizations/${orgId}/policy`),
  putPolicy: (orgId: string, p: Policy) => request<Policy>(`/organizations/${orgId}/policy`, { method: 'PUT', body: json(p) }),
  renameOrg: (orgId: string, name: string) => request<unknown>(`/organizations/${orgId}`, { method: 'PATCH', body: json({ name }) }),

  teams: () => request<Page<Team>>('/teams'),
  team: (id: string) => request<Team>(`/teams/${id}`),
  createTeam: (name: string, slug: string) => request<Team>('/teams', { method: 'POST', body: json({ name, slug }) }),
  updateTeam: (id: string, name: string, archived: boolean) => request<Team>(`/teams/${id}`, { method: 'PATCH', body: json({ name, archived }) }),
  putTeamMember: (id: string, userId: string, role: string) => request<unknown>(`/teams/${id}/members/${userId}`, { method: 'PUT', body: json({ role }) }),
  deleteTeamMember: (id: string, userId: string) => request<unknown>(`/teams/${id}/members/${userId}`, { method: 'DELETE' }),
  teamReviewers: (id: string) => request<Page<Reviewer>>(`/teams/${id}/reviewers`),
  putTeamReviewers: (id: string, user_ids: string[]) => request<Page<Reviewer>>(`/teams/${id}/reviewers`, { method: 'PUT', body: json({ user_ids }) }),

  projects: () => request<Page<Project>>('/projects'),
  project: (id: string) => request<Project>(`/projects/${id}`),
  createProject: (name: string, slug: string, team_id?: string) => request<Project>('/projects', { method: 'POST', body: json({ name, slug, team_id }) }),
  updateProject: (id: string, name: string, team_id: string, archived: boolean) => request<Project>(`/projects/${id}`, { method: 'PATCH', body: json({ name, team_id, archived }) }),
  putProjectMember: (id: string, userId: string, role: string) => request<unknown>(`/projects/${id}/members/${userId}`, { method: 'PUT', body: json({ role }) }),
  deleteProjectMember: (id: string, userId: string) => request<unknown>(`/projects/${id}/members/${userId}`, { method: 'DELETE' }),
  projectReviewers: (id: string) => request<Page<Reviewer>>(`/projects/${id}/reviewers`),
  putProjectReviewers: (id: string, user_ids: string[]) => request<Page<Reviewer>>(`/projects/${id}/reviewers`, { method: 'PUT', body: json({ user_ids }) }),

  members: () => request<Page<Member>>('/members'),
  createMember: (email: string, name: string, role: string, password: string) =>
    request<Member>('/members', { method: 'POST', body: json({ email, name, role, password }) }),
  setMemberStatus: (id: string, status: string) => request<Member>(`/members/${id}/status`, { method: 'POST', body: json({ status }) }),
  setMemberRole: (id: string, role: string) => request<Member>(`/members/${id}/role`, { method: 'POST', body: json({ role }) }),
  resetPassword: (id: string, password: string) => request<unknown>(`/members/${id}/password`, { method: 'POST', body: json({ password }) }),
  devices: (userId?: string) => request<Page<Device>>(`/devices${userId ? `?user_id=${userId}` : ''}`),
  revokeDevice: (id: string) => request<unknown>(`/devices/${id}`, { method: 'DELETE' }),
  bindings: (userId?: string) => request<Page<Binding>>(`/bindings${userId ? `?user_id=${userId}` : ''}`),
  enrollments: () => request<Page<Enrollment>>('/enrollments'),
  createEnrollment: (project_ids: string[], ttl_hours: number, max_uses: number) =>
    request<Enrollment>('/enrollments', { method: 'POST', body: json({ project_ids, ttl_hours, max_uses }) }),
  revokeEnrollment: (id: string) => request<unknown>(`/enrollments/${id}`, { method: 'DELETE' }),

  idpConfig: () => request<IdPConfig>('/idp/config'),
  putIdPConfig: (c: IdPConfig) => request<IdPConfig>('/idp/config', { method: 'PUT', body: json(c) }),
  idpMappings: () => request<Page<GroupMapping>>('/idp/mappings'),
  putIdPMappings: (items: GroupMapping[]) => request<Page<GroupMapping>>('/idp/mappings', { method: 'PUT', body: json({ items }) }),
  idpReconcile: () => request<Page<ReconcileDiff>>('/idp/reconcile'),
  applyIdPReconcile: (user_ids?: string[]) => request<{ applied: number }>('/idp/reconcile/apply', { method: 'POST', body: json({ user_ids }) }),
  runMaintenance: () => request<MaintenanceReport>('/maintenance/run', { method: 'POST' }),

  // ---- 内容 ----
  resources: (q: { level?: string; owner_id?: string; kind?: string; q?: string } = {}) =>
    request<Page<Resource>>(`/resources?${new URLSearchParams(Object.entries(q).filter(([, v]) => v) as [string, string][]).toString()}`),
  resource: (id: string) => request<ResourceDetail>(`/resources/${id}`),
  resourceVersion: (id: string) => request<Version & { resource: Resource }>(`/resource-versions/${id}`),
  setTags: (id: string, tags: string[]) => request<ResourceDetail>(`/resources/${id}/tags`, { method: 'PUT', body: json({ tags }) }),
  async uploadBlob(content: string) {
    const bytes = new TextEncoder().encode(content)
    const digest = await crypto.subtle.digest('SHA-256', bytes)
    const sha = 'sha256:' + Array.from(new Uint8Array(digest)).map((b) => b.toString(16).padStart(2, '0')).join('')
    await request<unknown>('/blobs', { method: 'POST', body: bytes, raw: true, headers: { 'X-Sha256': sha, 'Content-Type': 'application/octet-stream' } })
    return { sha256: sha, size: bytes.length }
  },
  blob: async (sha: string) => {
    const res = await fetch(`/v1/blobs/${sha}`, { headers: { Authorization: `Bearer ${getToken()}` } })
    if (!res.ok) throw new ApiError(res.status, 'NOT_FOUND', '内容不可见')
    return res.text()
  },

  changesets: (view: 'mine' | 'review' | 'all', state?: string) =>
    request<Page<Changeset>>(`/change-sets?view=${view}${state ? `&state=${state}` : ''}`),
  changeset: (id: string) => request<Changeset>(`/change-sets/${id}`),
  createChangeset: (body: { title: string; description?: string; ops: Op[]; fast_track?: boolean }) =>
    request<Changeset>('/change-sets', { method: 'POST', body: json(body) }),
  updateChangeset: (id: string, etag: string, body: { title: string; description?: string; ops: Op[]; fast_track?: boolean }) =>
    request<Changeset>(`/change-sets/${id}`, { method: 'PATCH', body: json(body), ifMatch: etag }),
  submitChangeset: (id: string) => request<Changeset>(`/change-sets/${id}/submit`, { method: 'POST' }),
  reviewChangeset: (id: string, decision: string, digest: string, comment: string) =>
    request<Changeset>(`/change-sets/${id}/reviews`, { method: 'POST', body: json({ decision, digest, comment }) }),
  publishChangeset: (id: string) => request<Release>(`/change-sets/${id}/publish`, { method: 'POST' }),
  cancelChangeset: (id: string) => request<unknown>(`/change-sets/${id}/cancel`, { method: 'POST' }),
  changesetDiff: (id: string) => request<Page<DiffItem>>(`/change-sets/${id}/diff`),
  releases: (q: { project_id?: string; team_id?: string; level?: string } = {}) =>
    request<Page<Release>>(`/releases?${new URLSearchParams(Object.entries(q).filter(([, v]) => v) as [string, string][]).toString()}`),
  rollback: (id: string, fast_track: boolean) => request<Changeset>(`/releases/${id}/rollback`, { method: 'POST', body: json({ fast_track }) }),

  groups: () => request<Page<Group>>('/groups'),
  createGroup: (key: string, name: string, description: string) => request<{ id: string }>('/groups', { method: 'POST', body: json({ key, name, description }) }),
  updateGroup: (id: string, name: string, description: string, archived: boolean) =>
    request<unknown>(`/groups/${id}`, { method: 'PATCH', body: json({ name, description, archived }) }),
  setGroupBundles: (id: string, bundle_ids: string[]) => request<{ count: number }>(`/groups/${id}/bundles`, { method: 'PUT', body: json({ bundle_ids }) }),
  assignments: () => request<Page<Assignment>>('/assignments'),
  createAssignment: (body: { bundle_id?: string; group_id?: string; subject_type: string; subject_id?: string; expires_at?: string }) =>
    request<{ id: string }>('/assignments', { method: 'POST', body: json(body) }),
  deleteAssignment: (id: string) => request<unknown>(`/assignments/${id}`, { method: 'DELETE' }),

  // ---- 知识与上报 ----
  learnings: (q: { project_id?: string; q?: string; status?: string } = {}) =>
    request<Page<Learning>>(`/learnings?${new URLSearchParams(Object.entries(q).filter(([, v]) => v) as [string, string][]).toString()}`),
  learning: (id: string) => request<Learning>(`/learnings/${id}`),
  contribute: (body: { project_id?: string; title: string; content: string; tags?: string[] }) =>
    request<Changeset>('/learnings', { method: 'POST', body: json(body) }),
  archiveLearning: (id: string) => request<Changeset>(`/learnings/${id}/archive`, { method: 'POST' }),
  promoteLearning: (id: string, kind: string, name: string) => request<Changeset>(`/learnings/${id}/promote`, { method: 'POST', body: json({ kind, name }) }),
  digest: (week?: string) => request<Digest>(`/reports/digest${week ? `?week=${week}` : ''}`),
  kbHealth: () => request<KBHealth>('/reports/kb-health'),

  // ---- 审计 ----
  auditEvents: (type?: string) => request<Page<AuditEvent>>(`/audit/events?limit=200${type ? `&type=${encodeURIComponent(type)}` : ''}`),
  distributions: (action?: string) => request<Page<Distribution>>(`/audit/distributions${action ? `?action=${action}` : ''}`),
  executions: () => request<Page<Execution>>('/audit/executions'),
}
