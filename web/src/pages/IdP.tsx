import { useEffect, useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { api, type GroupMapping, type IdPConfig, type ReconcileDiff } from '@/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Badge } from '@/components/ui/badge'
import { Empty, Err, fmtTime, PageHeader, TableSkeleton, useAsync } from '@/ui'

const EMPTY: IdPConfig = { issuer: '', client_id: '', client_secret: '', scopes: ['openid', 'email', 'profile', 'groups'], enabled: false }

export function IdP() {
  // 未配置时 scopes 为空，表单里补上常用默认值
  const cfg = useAsync(() => api.idpConfig().then((c) => ({ ...c, scopes: c.scopes?.length ? c.scopes : EMPTY.scopes })).catch(() => EMPTY))
  const mappings = useAsync(() => api.idpMappings())
  if (cfg.loading || mappings.loading) return <TableSkeleton />
  return (
    <>
      <PageHeader title="身份源" description="接入 OIDC 身份源后，成员用企业账号登录；IdP 分组可以映射到团队或项目的角色，登录时自动同步成员关系。" />
      <div className="grid gap-4 lg:grid-cols-2">
        <ConfigForm initial={cfg.data ?? EMPTY} onSaved={cfg.reload} />
        <MappingEditor initial={mappings.data?.items ?? []} />
      </div>
      <Reconcile />
    </>
  )
}

// Reconcile 展示映射表改动后已登录成员的角色漂移；管理员确认后一键应用，不自动改。
function Reconcile() {
  const { data, err, loading, reload } = useAsync(() => api.idpReconcile())
  const [busy, setBusy] = useState(false)
  async function apply(ids?: string[]) {
    setBusy(true)
    try {
      const r = await api.applyIdPReconcile(ids)
      toast.success(`已应用 ${r.applied} 位成员`)
      reload()
    } catch (e) { toast.error((e as Error).message) } finally { setBusy(false) }
  }
  const label = (c: ReconcileDiff['changes'][number]) => `${c.target}${c.target_id ? ' ' + c.target_id.slice(0, 8) : ''}: ${c.current || '—'} → ${c.expected || '移除'}`
  return (
    <Card className="mt-4"><CardContent className="p-5">
      <div className="mb-1 flex items-center gap-3"><h2 className="font-semibold">对账</h2>{!!data?.items.length && <Button size="sm" disabled={busy} onClick={() => apply()}>全部应用</Button>}</div>
      <p className="mb-3 text-xs text-muted-foreground">按最近一次登录时 IdP 给出的分组，用当前映射表重算角色。改了映射表之后，这里列出还没重新登录、角色已经落后的成员。</p>
      <Err msg={err} />
      {loading ? <TableSkeleton rows={2} /> : !data?.items.length ? <Empty>所有 OIDC 成员的角色都与映射表一致。</Empty> : (
        <ul className="divide-y text-sm">
          {data.items.map((d) => (
            <li key={d.user_id} className="flex flex-wrap items-center gap-2 py-2">
              <span className="min-w-0 flex-1 truncate">{d.email}<span className="ml-2 text-xs text-muted-foreground">上次同步 {fmtTime(d.synced_at)}</span></span>
              {d.changes.map((c, i) => <Badge key={i} variant="warning">{label(c)}</Badge>)}
              <Button variant="outline" size="sm" disabled={busy} onClick={() => apply([d.user_id])}>应用</Button>
            </li>
          ))}
        </ul>
      )}
    </CardContent></Card>
  )
}

function ConfigForm({ initial, onSaved }: { initial: IdPConfig; onSaved: () => void }) {
  const [c, setC] = useState<IdPConfig>({ ...initial, client_secret: '' })
  const [err, setErr] = useState('')
  useEffect(() => setC({ ...initial, client_secret: '' }), [initial])
  const set = (k: keyof IdPConfig) => (e: React.ChangeEvent<HTMLInputElement>) => setC({ ...c, [k]: e.target.value })
  async function save() {
    setErr('')
    try {
      await api.putIdPConfig({ ...c, client_secret: c.client_secret || undefined })
      toast.success('已保存')
      onSaved()
    } catch (e) { setErr((e as Error).message) }
  }
  const callback = `${location.origin}/v1/auth/oidc/callback`
  return (
    <Card><CardContent className="grid gap-3 p-5">
      <h2 className="font-semibold">OIDC 配置</h2>
      <div className="space-y-1"><Label htmlFor="idp-issuer">Issuer</Label><Input id="idp-issuer" value={c.issuer} onChange={set('issuer')} placeholder="https://login.example.com/realms/dev" /></div>
      <div className="space-y-1"><Label htmlFor="idp-client">Client ID</Label><Input id="idp-client" value={c.client_id} onChange={set('client_id')} /></div>
      <div className="space-y-1"><Label htmlFor="idp-secret">Client Secret</Label><Input id="idp-secret" type="password" value={c.client_secret ?? ''} onChange={set('client_secret')} placeholder={initial.id ? '留空则保持不变' : ''} autoComplete="new-password" /></div>
      <div className="space-y-1"><Label htmlFor="idp-scopes">Scopes</Label><Input id="idp-scopes" value={c.scopes.join(' ')} onChange={(e) => setC({ ...c, scopes: e.target.value.split(/\s+/).filter(Boolean) })} /></div>
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={c.enabled} onChange={(e) => setC({ ...c, enabled: e.target.checked })} />启用 OIDC 登录</label>
      <p className="text-xs text-muted-foreground">在 IdP 侧登记回调地址：<code className="rounded bg-muted px-1">{callback}</code></p>
      <Err msg={err} />
      <div><Button onClick={save} disabled={!c.issuer || !c.client_id}>保存</Button></div>
    </CardContent></Card>
  )
}

function MappingEditor({ initial }: { initial: GroupMapping[] }) {
  const [rows, setRows] = useState<GroupMapping[]>(initial)
  const [err, setErr] = useState('')
  const teams = useAsync(() => api.teams())
  const projects = useAsync(() => api.projects())
  const update = (i: number, patch: Partial<GroupMapping>) => setRows(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  async function save() {
    setErr('')
    try {
      const r = await api.putIdPMappings(rows)
      setRows(r.items)
      toast.success('已保存')
    } catch (e) { setErr((e as Error).message) }
  }
  return (
    <Card><CardContent className="p-5">
      <h2 className="font-semibold">分组映射</h2>
      <p className="mb-3 text-xs text-muted-foreground">按优先级从高到低匹配 IdP 的 groups claim。org 级映射决定组织角色；team / project 映射会把成员加进对应团队或项目。</p>
      <div className="grid gap-2">
        {rows.map((r, i) => (
          <div key={i} className="grid grid-cols-[1fr_auto_1fr_auto_auto_auto] items-center gap-2">
            <Input value={r.idp_group} onChange={(e) => update(i, { idp_group: e.target.value })} placeholder="IdP 分组名" />
            <Select value={r.target} onValueChange={(v) => update(i, { target: v, target_id: undefined })}><SelectTrigger className="w-24"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="org">org</SelectItem><SelectItem value="team">team</SelectItem><SelectItem value="project">project</SelectItem></SelectContent></Select>
            {r.target === 'org' ? <span className="text-xs text-muted-foreground">整个组织</span> : (
              <Select value={r.target_id ?? ''} onValueChange={(v) => update(i, { target_id: v })}><SelectTrigger><SelectValue placeholder="选择" /></SelectTrigger><SelectContent>{(r.target === 'team' ? teams.data?.items : projects.data?.items)?.map((t) => <SelectItem key={t.id} value={t.id}>{t.name}</SelectItem>)}</SelectContent></Select>
            )}
            <Select value={r.role} onValueChange={(v) => update(i, { role: v })}><SelectTrigger className="w-28"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="admin">admin</SelectItem><SelectItem value="member">member</SelectItem>{r.target === 'org' && <SelectItem value="viewer">viewer</SelectItem>}</SelectContent></Select>
            <Input type="number" className="w-20" value={r.priority} onChange={(e) => update(i, { priority: Number(e.target.value) })} title="优先级" />
            <Button variant="ghost" size="icon" className="text-destructive" onClick={() => setRows(rows.filter((_, j) => j !== i))}><Trash2 /></Button>
          </div>
        ))}
        {rows.length === 0 && <p className="text-sm text-muted-foreground">还没有映射，所有 OIDC 登录的成员都按默认角色加入。</p>}
      </div>
      <Err msg={err} />
      <div className="mt-3 flex gap-2">
        <Button variant="outline" onClick={() => setRows([...rows, { idp_group: '', target: 'org', role: 'member', priority: rows.length }])}><Plus />添加</Button>
        <Button onClick={save}>保存映射</Button>
      </div>
    </CardContent></Card>
  )
}
