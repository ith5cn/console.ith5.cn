// 与 Go 服务端 DTO 对应的类型。
//
// 服务端是 Go，前端是 TS，因此这份类型是手写的。契约变更时两边要一起改；
// 若接口数量继续增长，应改为从 Go struct 生成（见技术方案 D12 的讨论）。

export interface UserInfo { id: string; email: string; role: string; org_id: string }
export interface GrantSource { subject_type: string; group_name?: string }

export interface Bundle {
  id: string; name: string; kind: string; description: string
  archived: boolean; latest_version: number; checksum: string
  groups?: string[]; updated_at: string
}
export interface FileItem { path: string; content: string }
export interface BundleDetail extends Bundle { draft_files: FileItem[] }

export interface VersionInfo {
  version: number; checksum: string; changelog: string
  // 非零表示本版是对该版本的回滚。changelog 是自由文本，承担不了这个职责。
  rollback_of_version?: number
  published_by: string; published_at: string
}

export interface Group {
  id: string; key: string; name: string; description: string
  archived: boolean; bundle_ids: string[]; bundle_names: string[]
  assigned_to: number
}

export interface Assignment {
  id: string; bundle_id?: string; bundle_name?: string
  group_id?: string; group_name?: string
  subject_type: string; subject_id?: string; subject_name?: string
  expires_at?: string; expired: boolean; created_at: string
}

export interface Member {
  id: string; email: string; name: string; role: string
  status: string; machines: number; last_seen_at?: string
}

export interface AuditEntry {
  email: string; hostname: string; bundle_name: string
  version: number; action: string; detail?: string; created_at: string
}

// ExecutionEntry 的字段集合即隐私白名单：
// 服务端只落这些字段，文件内容、diff、完整命令、提示词一律不上报。
export interface ExecutionEntry {
  email: string; hostname: string; session_id: string
  event_type: string; tool_name?: string
  summary: {
    repo?: string; file_path?: string; bash_command?: string
    lines_changed?: number; exit_code?: number
  }
  occurred_at: string
}

export interface StaleMachine {
  email: string; hostname: string; last_seen_at: string
  days: number; user_level: boolean
}

export interface ExplainEntry {
  bundle_id: string; bundle_name: string; kind: string
  version: number; via: GrantSource[]
}

const TOKEN_KEY = 'ith5_token'
const USER_KEY = 'ith5_user'
export const DEMO_READ_ONLY_MESSAGE = '当前是演示环境，无法修改'

export function getToken() { return localStorage.getItem(TOKEN_KEY) }
export function getUser(): UserInfo | null {
  const raw = localStorage.getItem(USER_KEY)
  return raw ? JSON.parse(raw) : null
}
export function clearAuth() {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(USER_KEY)
}

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message)
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const method = (init?.method ?? 'GET').toUpperCase()
  if (getUser()?.role === 'viewer' && !['GET', 'HEAD', 'OPTIONS'].includes(method)) {
    throw new ApiError(403, 'demo_read_only', DEMO_READ_ONLY_MESSAGE)
  }
  const token = getToken()
  const res = await fetch(`/api/v1${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(init?.headers ?? {}),
    },
  })
  if (res.status === 204) return undefined as T
  const body = await res.json().catch(() => ({}))
  if (!res.ok) {
    // 令牌失效时清掉本地状态，让界面回到登录页
    if (res.status === 401) clearAuth()
    throw new ApiError(res.status, body.error ?? 'error', body.message ?? res.statusText)
  }
  return body as T
}

export const api = {
  async login(org_slug: string, email: string, password: string) {
    const r = await request<{ access_token: string; user: UserInfo }>(
      '/auth/login', { method: 'POST', body: JSON.stringify({ org_slug, email, password }) })
    localStorage.setItem(TOKEN_KEY, r.access_token)
    localStorage.setItem(USER_KEY, JSON.stringify(r.user))
    return r.user
  },

  // 设备激活：CLI 登录流程的 Web 一环
  activate(user_code: string, org_slug: string, email: string, password: string) {
    return request<{ status: string }>('/auth/device/activate',
      { method: 'POST', body: JSON.stringify({ user_code, org_slug, email, password }) })
  },

  listBundles: () => request<{ bundles: Bundle[] }>('/admin/bundles'),
  getBundle: (id: string) => request<BundleDetail>(`/admin/bundles/${id}`),
  // content 可选：留空时服务端按 kind 种一份模板，
  // 保证新建出来的编辑器里已有一个合法的 SKILL.md
  createBundle: (name: string, kind: string, description: string, content: string) =>
    request<{ id: string }>('/admin/bundles', {
      method: 'POST',
      body: JSON.stringify({ name, kind, description, content }),
    }),
  saveDraft: (id: string, files: FileItem[]) =>
    request<{ status: string }>(`/admin/bundles/${id}/draft`, { method: 'PUT', body: JSON.stringify({ files }) }),
  publish: (id: string, changelog: string) =>
    request<{ version: number }>(`/admin/bundles/${id}/publish`, { method: 'POST', body: JSON.stringify({ changelog }) }),
  listVersions: (id: string) => request<{ versions: VersionInfo[] }>(`/admin/bundles/${id}/versions`),
  rollback: (id: string, version: number) =>
    request<{ version: number; rollback_of: number }>(`/admin/bundles/${id}/rollback/${version}`, { method: 'POST' }),
  archiveBundle: (id: string, archived: boolean) =>
    request<{ archived: boolean }>(`/admin/bundles/${id}/archive`, { method: 'POST', body: JSON.stringify({ archived }) }),

  listGroups: () => request<{ groups: Group[] }>('/admin/groups'),
  createGroup: (key: string, name: string, description: string) =>
    request<{ id: string }>('/admin/groups', { method: 'POST', body: JSON.stringify({ key, name, description }) }),
  setGroupBundles: (id: string, bundle_ids: string[]) =>
    request<{ count: number }>(`/admin/groups/${id}/bundles`, { method: 'PUT', body: JSON.stringify({ bundle_ids }) }),

  listAssignments: () => request<{ assignments: Assignment[] }>('/admin/assignments'),
  createAssignment: (body: Record<string, unknown>) =>
    request<{ id: string }>('/admin/assignments', { method: 'POST', body: JSON.stringify(body) }),
  deleteAssignment: (id: string) => request<void>(`/admin/assignments/${id}`, { method: 'DELETE' }),

  listMembers: () => request<{ members: Member[] }>('/admin/members'),
  setMemberStatus: (id: string, status: string) =>
    request<{ status: string }>(`/admin/members/${id}/status`, { method: 'POST', body: JSON.stringify({ status }) }),
  // 没有邮件设施，所以建号时管理员直接设初始密码，线下交给员工
  createMember: (email: string, name: string, role: string, password: string) =>
    request<{ id: string }>('/admin/members', {
      method: 'POST',
      body: JSON.stringify({ email, name, role, password }),
    }),
  resetMemberPassword: (id: string, password: string) =>
    request<void>(`/admin/members/${id}/password`, { method: 'POST', body: JSON.stringify({ password }) }),

  audit: (action?: string) =>
    request<{ entries: AuditEntry[] }>(`/admin/audit/distributions${action ? `?action=${action}` : ''}`),
  executions: () => request<{ entries: ExecutionEntry[] }>('/admin/audit/executions'),
  staleMachines: () => request<{ machines: StaleMachine[] }>('/admin/health/stale-machines'),
  explain: (user_id: string) =>
    request<{ user: UserInfo; suspended: boolean; grants: ExplainEntry[] }>(`/admin/explain?user_id=${user_id}`),
}
