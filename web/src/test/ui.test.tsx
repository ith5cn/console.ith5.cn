import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, setSession, type Me, type Membership } from '@/api'
import { AppShell } from '@/components/AppShell'
import { Login } from '@/pages/Login'
import { parseHash } from '@/navigation'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  localStorage.clear()
})

const account = { id: 'a1', email: 'owner@example.com', name: 'Owner' }
const membership: Membership = { user_id: 'u1', org_id: 'o1', org_slug: 'acme', org_name: 'Acme', role: 'owner' }
const me: Me = { account, membership, token_kind: 'session', permissions: ['project:read', 'resource:write', 'membership:manage', 'audit:read'] }

describe('Login', () => {
  it('logs straight in when the account belongs to one organization', async () => {
    vi.spyOn(api, 'login').mockResolvedValue({ account, login_token: 'lt', organizations: [membership] })
    vi.spyOn(api, 'session').mockResolvedValue(me)
    const onDone = vi.fn()
    render(<Login onDone={onDone} />)
    await userEvent.type(screen.getByLabelText('邮箱'), account.email)
    await userEvent.type(screen.getByLabelText('密码'), 'password')
    await userEvent.click(screen.getByRole('button', { name: /进入控制台/ }))
    await waitFor(() => expect(onDone).toHaveBeenCalledWith(me))
    expect(api.session).toHaveBeenCalledWith('lt', 'u1')
  })

  it('asks which organization to enter when there are several', async () => {
    const other: Membership = { ...membership, user_id: 'u2', org_id: 'o2', org_slug: 'beta', org_name: 'Beta', role: 'member' }
    vi.spyOn(api, 'login').mockResolvedValue({ account, login_token: 'lt', organizations: [membership, other] })
    vi.spyOn(api, 'session').mockResolvedValue(me)
    const onDone = vi.fn()
    render(<Login onDone={onDone} />)
    await userEvent.type(screen.getByLabelText('邮箱'), account.email)
    await userEvent.type(screen.getByLabelText('密码'), 'password')
    await userEvent.click(screen.getByRole('button', { name: /进入控制台/ }))
    await userEvent.click(await screen.findByRole('button', { name: /Beta/ }))
    await waitFor(() => expect(onDone).toHaveBeenCalledWith(me))
    expect(api.session).toHaveBeenCalledWith('lt', 'u2')
  })

  it('shows the API error and re-enables the form', async () => {
    vi.spyOn(api, 'login').mockRejectedValue(new Error('账号或密码错误'))
    render(<Login onDone={() => {}} />)
    await userEvent.type(screen.getByLabelText('邮箱'), account.email)
    await userEvent.type(screen.getByLabelText('密码'), 'password')
    await userEvent.click(screen.getByRole('button', { name: /进入控制台/ }))
    expect(await screen.findByText('账号或密码错误')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /进入控制台/ })).toBeEnabled()
  })
})

describe('AppShell', () => {
  it('hides navigation the member has no permission for', () => {
    setSession('t', { ...me, permissions: ['project:read'] })
    render(<AppShell me={{ ...me, permissions: ['project:read'] }} page="overview" onLogout={() => {}}><div>content</div></AppShell>)
    expect(screen.getAllByText('内容库').length).toBeGreaterThan(0)
    expect(screen.queryByText('审计')).not.toBeInTheDocument()
    expect(screen.queryByText('身份源')).not.toBeInTheDocument()
  })

  it('shows a read-only banner for viewers', () => {
    const viewer: Me = { ...me, membership: { ...membership, role: 'viewer' }, permissions: ['project:read'] }
    setSession('t', viewer)
    render(<AppShell me={viewer} page="overview" onLogout={() => {}}><div>content</div></AppShell>)
    expect(screen.getByText(/只读/)).toBeInTheDocument()
  })
})

describe('hash routing', () => {
  it('parses page and id, falling back to overview', () => {
    expect(parseHash('#/changesets/abc')).toEqual({ page: 'changesets', id: 'abc' })
    expect(parseHash('#/resources')).toEqual({ page: 'resources', id: undefined })
    expect(parseHash('#/nope')).toEqual({ page: 'overview' })
    expect(parseHash('')).toEqual({ page: 'overview' })
  })
})
