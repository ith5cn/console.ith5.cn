import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, DEMO_READ_ONLY_MESSAGE, type UserInfo } from '@/api'
import { AppShell } from '@/components/AppShell'
import { Login } from '@/pages/Login'
import { Overview } from '@/pages/Overview'
import { toCSV } from '@/ui'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  localStorage.clear()
})

describe('Login', () => {
  const user: UserInfo = { id: 'u1', email: 'owner@example.com', role: 'owner', org_id: 'o1' }

  it('submits credentials and returns the logged-in user', async () => {
    vi.spyOn(api, 'login').mockResolvedValue(user)
    const onDone = vi.fn()
    render(<Login onDone={onDone} />)
    await userEvent.type(screen.getByLabelText('邮箱'), user.email)
    await userEvent.type(screen.getByLabelText('密码'), 'password')
    await userEvent.click(screen.getByRole('button', { name: /进入控制台/ }))
    await waitFor(() => expect(onDone).toHaveBeenCalledWith(user))
    expect(api.login).toHaveBeenCalledWith('demo', user.email, 'password')
  })

  it('shows an API error and prevents duplicate submits while pending', async () => {
    let rejectLogin!: (reason: Error) => void
    vi.spyOn(api, 'login').mockImplementation(() => new Promise((_, reject) => { rejectLogin = reject }))
    render(<Login onDone={() => {}} />)
    await userEvent.type(screen.getByLabelText('邮箱'), user.email)
    await userEvent.type(screen.getByLabelText('密码'), 'password')
    await userEvent.click(screen.getByRole('button', { name: /进入控制台/ }))
    expect(screen.getByRole('button', { name: '登录中…' })).toBeDisabled()
    rejectLogin(new Error('账号或密码错误'))
    expect(await screen.findByText('账号或密码错误')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /进入控制台/ })).toBeEnabled()
  })
})

describe('AppShell', () => {
  const shellProps = { user: { id: 'u', email: 'owner@example.com', role: 'owner', org_id: 'o' }, page: 'overview' as const, onLogout: () => {} }

  it('emits the selected navigation page', async () => {
    const onPageChange = vi.fn()
    render(<AppShell {...shellProps} onPageChange={onPageChange}><div>content</div></AppShell>)
    await userEvent.click(screen.getAllByRole('button', { name: '内容库' })[0])
    expect(onPageChange).toHaveBeenCalledWith('bundles')
  })

  it('closes the mobile sheet after navigation', async () => {
    const onPageChange = vi.fn()
    render(<AppShell {...shellProps} onPageChange={onPageChange}><div>content</div></AppShell>)
    await userEvent.click(screen.getByRole('button', { name: '打开导航' }))
    const sheet = screen.getByRole('dialog')
    await userEvent.click(within(sheet).getByRole('button', { name: '内容库' }))
    expect(onPageChange).toHaveBeenCalledWith('bundles')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('shows a read-only banner for demo viewers', () => {
    render(<AppShell {...shellProps} user={{ ...shellProps.user, role: 'viewer' }} onPageChange={() => {}}><div>content</div></AppShell>)
    expect(screen.getByText('当前是演示环境，你可以浏览所有页面，但无法修改数据。')).toBeInTheDocument()
  })
})

describe('Demo viewer API', () => {
  function setViewer() {
    localStorage.setItem('ith5_token', 'demo-token')
    localStorage.setItem('ith5_user', JSON.stringify({ id: 'u', email: 'demo@example.com', role: 'viewer', org_id: 'o' }))
  }

  it('blocks admin mutations before sending a network request', async () => {
    setViewer()
    const fetchMock = vi.spyOn(globalThis, 'fetch')

    await expect(api.createGroup('demo', 'Demo', '')).rejects.toThrow(DEMO_READ_ONLY_MESSAGE)
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('allows device activation while a viewer session exists', async () => {
    setViewer()
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(
      JSON.stringify({ status: 'approved' }),
      { status: 200, headers: { 'Content-Type': 'application/json' } },
    ))

    await expect(api.activate('ABCD-EFGH', 'demo', 'test@ith5.cn', 'password')).resolves.toEqual({ status: 'approved' })
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/device/activate', expect.objectContaining({ method: 'POST' }))
  })
})

describe('Overview', () => {
  function mockHealthyOverview() {
    vi.spyOn(api, 'listGroups').mockResolvedValue({ groups: [] })
    vi.spyOn(api, 'listAssignments').mockResolvedValue({ assignments: [] })
    vi.spyOn(api, 'listMembers').mockResolvedValue({ members: [] })
    vi.spyOn(api, 'staleMachines').mockResolvedValue({ machines: [] })
    vi.spyOn(api, 'audit').mockResolvedValue({ entries: [] })
  }

  it('derives dashboard metrics from existing APIs', async () => {
    mockHealthyOverview()
    vi.spyOn(api, 'listBundles').mockResolvedValue({ bundles: [{ id: 'b', name: 'one', kind: 'skill', description: '', archived: false, latest_version: 2, checksum: '', updated_at: '' }] })
    render(<Overview />)
    expect(await screen.findByText('1 项内容')).toBeInTheDocument()
    expect(screen.getByText('暂无分发事件')).toBeInTheDocument()
  })

  it('keeps healthy regions visible after a partial API failure', async () => {
    mockHealthyOverview()
    vi.spyOn(api, 'listBundles').mockRejectedValue(new Error('内容服务不可用'))
    render(<Overview />)
    expect(await screen.findByText('内容服务不可用')).toBeInTheDocument()
    expect(screen.getByText('暂无分发事件')).toBeInTheDocument()
  })
})

describe('CSV export', () => {
  it('adds a BOM and neutralizes spreadsheet formulas', () => {
    const csv = toCSV([{ value: '=SUM(1,1)' }], [['value', '值']])
    expect(csv.startsWith('\uFEFF')).toBe(true)
    expect(csv).toContain("'=SUM(1,1)")
  })
})
