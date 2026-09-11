import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, setSession, type Me } from '@/api'
import { Audit } from '@/pages/Audit'
import { Digest } from '@/pages/Digest'
import { IdP } from '@/pages/IdP'
import { KBHealth } from '@/pages/KBHealth'
import { Knowledge } from '@/pages/Knowledge'
import { Policy } from '@/pages/Policy'

// 每个页面在「空数据」下都必须能渲染出标题：这是最容易被 null 字段击穿的地方。
const me: Me = {
  account: { id: 'a1', email: 'owner@example.com', name: 'Owner' },
  membership: { user_id: 'u1', org_id: 'o1', org_slug: 'acme', org_name: 'Acme', role: 'owner' },
  token_kind: 'session',
  permissions: ['project:read', 'resource:write', 'release:publish', 'audit:read', 'identity:manage', 'organization:manage'],
}
const page = { items: [] }

beforeEach(() => {
  setSession('t', me)
  vi.spyOn(api, 'projects').mockResolvedValue(page)
  vi.spyOn(api, 'teams').mockResolvedValue(page)
})
afterEach(() => { cleanup(); vi.restoreAllMocks(); localStorage.clear() })

describe('pages render with empty data', () => {
  it('Digest', async () => {
    vi.spyOn(api, 'digest').mockResolvedValue({ week_start: '2026-09-07', sessions: 0, sessions_succeeded: 0, prompt_turns: 0, active_ms: 0, cost_micros: 0, corrections: 0, cache_read_tokens: 0, active_members: 0, top_skills: [], highlights: [] })
    render(<Digest />)
    expect(await screen.findByRole('heading', { name: '周报' })).toBeInTheDocument()
  })

  it('KBHealth', async () => {
    vi.spyOn(api, 'kbHealth').mockResolvedValue({ by_kind: {}, top_recalled: [], silent: [], prune_candidates: [], promote_candidates: [], by_author: {}, recall_trend: [] })
    render(<KBHealth />)
    expect(await screen.findByRole('heading', { name: '知识库健康' })).toBeInTheDocument()
  })

  it('Audit', async () => {
    vi.spyOn(api, 'auditEvents').mockResolvedValue(page)
    render(<Audit />)
    expect(await screen.findByRole('heading', { name: '审计' })).toBeInTheDocument()
    expect(await screen.findByText('没有记录。')).toBeInTheDocument()
  })

  it('IdP with an unconfigured provider', async () => {
    vi.spyOn(api, 'idpConfig').mockResolvedValue({ issuer: '', client_id: '', scopes: [], enabled: false })
    vi.spyOn(api, 'idpMappings').mockResolvedValue(page)
    vi.spyOn(api, 'idpReconcile').mockResolvedValue(page)
    render(<IdP />)
    expect(await screen.findByRole('heading', { name: '身份源' })).toBeInTheDocument()
    expect(screen.getByLabelText('Scopes')).toHaveValue('openid email profile groups')
  })

  it('Policy', async () => {
    vi.spyOn(api, 'policy').mockResolvedValue({ required_approvals: 1, learnings_review: false, confidence_prune: 0.15, confidence_promote: 0.7, retention_months: 13 })
    render(<Policy />)
    expect(await screen.findByRole('heading', { name: '组织策略' })).toBeInTheDocument()
    expect(screen.getByLabelText('需要的批准数')).toHaveValue(1)
  })

  it('Knowledge', async () => {
    vi.spyOn(api, 'learnings').mockResolvedValue(page)
    render(<Knowledge />)
    expect(await screen.findByRole('heading', { name: '知识库' })).toBeInTheDocument()
    expect(await screen.findByText('没有匹配的经验。')).toBeInTheDocument()
  })
})
