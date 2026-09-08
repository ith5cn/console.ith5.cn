import { useState } from 'react'
import { Layers3, Plus, Settings2 } from 'lucide-react'
import { toast } from 'sonner'
import { api, type Group } from '../api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Empty, Err, PageHeader, TableSkeleton, useAsync } from '../ui'

export function Groups() {
  const groups = useAsync(() => api.listGroups()); const bundles = useAsync(() => api.listBundles()); const [editing, setEditing] = useState<Group | null>(null); const [creating, setCreating] = useState(false)
  return <><PageHeader title="权限组" description={<>权限组装配内容，再授权给成员。组内内容变化后，被授权者下次同步自动获得更新。</>} action={<Button onClick={() => setCreating(true)}><Plus />新建权限组</Button>} /><Create open={creating} onOpenChange={setCreating} onDone={() => { setCreating(false); groups.reload() }} /><Err msg={groups.err} />{groups.loading ? <TableSkeleton /> : !groups.data?.groups.length ? <Empty>还没有权限组</Empty> : <div className="grid gap-4 md:grid-cols-2">{groups.data.groups.map((g) => <Card key={g.id}><CardHeader><CardTitle className="flex items-center gap-2"><Layers3 className="size-4 text-primary" />{g.name}{g.archived && <Badge variant="warning">已归档</Badge>}</CardTitle><p className="font-mono text-xs text-muted-foreground">{g.key}</p></CardHeader><CardContent><p className="min-h-5 text-sm text-muted-foreground">{g.description || '暂无说明'}</p><div className="mt-4 flex flex-wrap gap-1.5">{g.bundle_names.length ? g.bundle_names.slice(0, 4).map((n) => <Badge key={n} variant="outline">{n}</Badge>) : <Badge>空组</Badge>}{g.bundle_names.length > 4 && <Badge>+{g.bundle_names.length - 4}</Badge>}</div></CardContent><CardFooter className="border-t pt-4 text-xs text-muted-foreground"><span>{g.bundle_names.length} 项内容 · {g.assigned_to} 条授权</span><Button variant="ghost" size="sm" className="ml-auto" onClick={() => setEditing(g)}><Settings2 />装配</Button></CardFooter></Card>)}</div>}{editing && bundles.data && <Assemble group={editing} all={bundles.data.bundles} open={!!editing} onOpenChange={(open) => !open && setEditing(null)} onDone={() => { setEditing(null); groups.reload() }} />}</>
}

function Create({ open, onOpenChange, onDone }: { open: boolean; onOpenChange: (v: boolean) => void; onDone: () => void }) {
  const [key, setKey] = useState(''); const [name, setName] = useState(''); const [desc, setDesc] = useState(''); const [err, setErr] = useState(''); const [busy, setBusy] = useState(false)
  async function submit(e: React.FormEvent) { e.preventDefault(); setBusy(true); setErr(''); try { await api.createGroup(key, name, desc); toast.success('权限组已创建'); onDone() } catch (error) { setErr((error as Error).message) } finally { setBusy(false) } }
  return <Dialog open={open} onOpenChange={onOpenChange}><DialogContent><DialogHeader><DialogTitle>新建权限组</DialogTitle><DialogDescription>用稳定标识组织一组可持续更新的内容。</DialogDescription></DialogHeader><form onSubmit={submit} className="space-y-4"><div className="space-y-2"><Label htmlFor="group-key">标识</Label><Input id="group-key" value={key} onChange={(e) => setKey(e.target.value)} placeholder="backend-pack" required autoFocus /></div><div className="space-y-2"><Label htmlFor="group-name">显示名</Label><Input id="group-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="后端工具包" required /></div><div className="space-y-2"><Label htmlFor="group-desc">说明</Label><Input id="group-desc" value={desc} onChange={(e) => setDesc(e.target.value)} /></div><Err msg={err} /><DialogFooter><Button type="button" variant="outline" onClick={() => onOpenChange(false)}>取消</Button><Button disabled={busy || !key || !name}>{busy ? '创建中…' : '创建'}</Button></DialogFooter></form></DialogContent></Dialog>
}

function Assemble({ group, all, open, onOpenChange, onDone }: { group: Group; all: { id: string; name: string; kind: string }[]; open: boolean; onOpenChange: (v: boolean) => void; onDone: () => void }) {
  const [sel, setSel] = useState<string[]>(group.bundle_ids); const [err, setErr] = useState(''); const [busy, setBusy] = useState(false)
  const toggle = (id: string) => setSel(sel.includes(id) ? sel.filter((x) => x !== id) : [...sel, id])
  async function save() { setBusy(true); setErr(''); try { await api.setGroupBundles(group.id, sel); toast.success('权限组装配已更新'); onDone() } catch (error) { setErr((error as Error).message) } finally { setBusy(false) } }
  return <Sheet open={open} onOpenChange={onOpenChange}><SheetContent><SheetHeader><SheetTitle>装配「{group.name}」</SheetTitle><SheetDescription>一个内容可以同时属于多个权限组，保存后下次同步生效。</SheetDescription></SheetHeader><div className="grid gap-2">{all.map((b) => <label key={b.id} className="flex cursor-pointer items-center gap-3 rounded-lg border p-3 hover:bg-muted"><input type="checkbox" className="size-4 accent-primary" checked={sel.includes(b.id)} onChange={() => toggle(b.id)} /><span className="min-w-0 flex-1 truncate font-medium">{b.name}</span><Badge>{b.kind}</Badge></label>)}</div><Err msg={err} /><div className="mt-auto flex justify-end gap-2 border-t pt-5"><Button variant="outline" onClick={() => onOpenChange(false)}>取消</Button><Button onClick={save} disabled={busy}>{busy ? '保存中…' : `保存（${sel.length} 项）`}</Button></div></SheetContent></Sheet>
}
