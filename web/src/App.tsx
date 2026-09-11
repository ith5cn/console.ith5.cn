import { useEffect, useState, type JSX } from 'react'
import { api, clearSession, getMe, getToken, type Me } from '@/api'
import { AppShell } from '@/components/AppShell'
import { Toaster } from '@/components/ui/toaster'
import { parseHash, type Page } from '@/navigation'
import { Activate } from '@/pages/Activate'
import { Assignments } from '@/pages/Assignments'
import { Audit } from '@/pages/Audit'
import { Changesets } from '@/pages/Changesets'
import { Digest } from '@/pages/Digest'
import { Groups } from '@/pages/Groups'
import { IdP } from '@/pages/IdP'
import { KBHealth } from '@/pages/KBHealth'
import { Knowledge } from '@/pages/Knowledge'
import { Login } from '@/pages/Login'
import { Members } from '@/pages/Members'
import { OIDCReturn } from '@/pages/OIDCReturn'
import { Overview } from '@/pages/Overview'
import { Policy } from '@/pages/Policy'
import { Projects } from '@/pages/Projects'
import { Releases } from '@/pages/Releases'
import { Resources } from '@/pages/Resources'
import { Teams } from '@/pages/Teams'

function useHashRoute() {
  const [route, setRoute] = useState(() => parseHash(location.hash))
  useEffect(() => {
    const onChange = () => setRoute(parseHash(location.hash))
    window.addEventListener('hashchange', onChange)
    return () => window.removeEventListener('hashchange', onChange)
  }, [])
  return route
}

const PAGES: Record<Page, (props: { id?: string }) => JSX.Element> = {
  overview: Overview, resources: Resources, changesets: Changesets, releases: Releases,
  groups: Groups, assignments: Assignments, teams: Teams, projects: Projects, members: Members,
  knowledge: Knowledge, digest: Digest, 'kb-health': KBHealth, audit: Audit, idp: IdP, policy: Policy,
}

export function App() {
  const [me, setMe] = useState<Me | null>(getToken() ? getMe() : null)
  const route = useHashRoute()

  // 会话令牌只有 15 分钟；页面打开时校验一次，失效就回登录页
  useEffect(() => {
    if (!me) return
    api.me().then(setMe, () => { clearSession(); setMe(null) })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const path = location.pathname
  if (path === '/activate') return <><Activate me={me} onLogin={setMe} /><Toaster /></>
  if (path === '/login/oidc') return <><OIDCReturn onDone={setMe} /><Toaster /></>
  if (!me) return <><Login onDone={setMe} /><Toaster /></>

  const Current = PAGES[route.page]
  return (
    <AppShell me={me} page={route.page} onLogout={() => { clearSession(); setMe(null) }}>
      <Current id={route.id} />
      <Toaster />
    </AppShell>
  )
}
