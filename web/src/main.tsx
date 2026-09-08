import { StrictMode, useState } from 'react'
import { createRoot } from 'react-dom/client'
import './styles.css'
import { api, clearAuth, getToken, getUser, type UserInfo } from './api'
import { Bundles } from './pages/Bundles'
import { Groups } from './pages/Groups'
import { Assignments } from './pages/Assignments'
import { Members } from './pages/Members'
import { Audit } from './pages/Audit'
import { Explain } from './pages/Explain'
import { Activate } from './pages/Activate'
import { Err } from './ui'

type Page = 'bundles' | 'groups' | 'assignments' | 'members' | 'audit' | 'explain'

const PAGES: { id: Page; label: string }[] = [
  { id: 'bundles', label: '内容' },
  { id: 'groups', label: '权限组' },
  { id: 'assignments', label: '授权' },
  { id: 'members', label: '成员' },
  { id: 'audit', label: '审计' },
  { id: 'explain', label: '授权解释器' },
]

function Login({ onDone }: { onDone: (u: UserInfo) => void }) {
  const [org, setOrg] = useState('demo')
  const [email, setEmail] = useState('')
  const [pw, setPw] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true); setErr('')
    try { onDone(await api.login(org, email, pw)) }
    catch (e) { setErr((e as Error).message) }
    finally { setBusy(false) }
  }

  return (
    <form className="login panel" onSubmit={submit}>
      <h1>ITH5 管理后台</h1>
      <p className="sub">登录以管理内容分发</p>
      <label>组织</label>
      <input value={org} onChange={(e) => setOrg(e.target.value)} autoComplete="organization" />
      <label>邮箱</label>
      <input value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="username" />
      <label>密码</label>
      <input type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="current-password" />
      <Err msg={err} />
      <div style={{ marginTop: 14 }}>
        <button className="btn primary" disabled={busy}>{busy ? '登录中…' : '登录'}</button>
      </div>
    </form>
  )
}

function App() {
  const [user, setUser] = useState<UserInfo | null>(getToken() ? getUser() : null)
  const [page, setPage] = useState<Page>('bundles')

  // /activate 是 CLI 登录流程的一环，不需要先登录后台
  if (location.pathname === '/activate') return <Activate />
  if (!user) return <Login onDone={setUser} />

  return (
    <div className="layout">
      <aside className="sidebar">
        <div className="brand">ITH5</div>
        <nav className="nav">
          {PAGES.map((p) => (
            <button key={p.id} className={page === p.id ? 'active' : ''} onClick={() => setPage(p.id)}>
              {p.label}
            </button>
          ))}
        </nav>
        <div className="who">
          {user.email}
          <br />
          <button className="btn" style={{ marginTop: 8 }}
            onClick={() => { clearAuth(); setUser(null) }}>登出</button>
        </div>
      </aside>
      <main className="main">
        {page === 'bundles' && <Bundles />}
        {page === 'groups' && <Groups />}
        {page === 'assignments' && <Assignments />}
        {page === 'members' && <Members />}
        {page === 'audit' && <Audit />}
        {page === 'explain' && <Explain />}
      </main>
    </div>
  )
}

createRoot(document.getElementById('root')!).render(<StrictMode><App /></StrictMode>)
