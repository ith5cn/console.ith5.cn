import { useState } from 'react'
import { Plus } from 'lucide-react'
import { toast } from 'sonner'
import { api, can, type Group } from '@/api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Empty, Err, PageHeader, TableSkeleton, useAsync } from '@/ui'

export function Groups() {
  const { data, err, loading, reload } = useAsync(() => api.groups())
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<Group | null>(null)
  const manage = can('membership:manage')

  async function archive(g: Group) {
    try {
      await api.updateGroup(g.id, g.name, g.description, !g.archived)
      toast.success(g.archived ? '已恢复' : '已归档')
      reload()
    } catch (e) { toast.error((e as Error).message) }
  }

  return (
    <>
      <PageHeader
        title="权限组"
        description="有名字的可选内容包。放进任一权限组的资源不再按层级自动分发，只发给被授权的人或项目。对 teamai 客户端它就是一个 role 命名空间。"
        action={manage && <Button onClick={() => setCreating(true)}><Plus />新建权限组</Button>}
      />
      <Err msg={err} />
      {loading ? <TableSkeleton /> : !data?.items.length ? <Empty>还没有权限组。</Empty> : (
        <Card className="overflow-hidden">
          <Table>
            <TableHeader><TableRow><TableHead>权限组</TableHead><TableHead>内容</TableHead><TableHead>授权数</TableHead><TableHead /></TableRow></TableHeader>
            <TableBody>
              {data.items.map((g) => (
                <TableRow key={g.id} className={g.archived ? 'opacity-60' : ''}>
                  <TableCell><div className="font-medium">{g.name}{g.archived && <Badge variant="secondary" className="ml-2">已归档</Badge>}</div><div className="font-mono text-xs text-muted-foreground">{g.key}</div>{g.description && <div className="text-xs text-muted-foreground">{g.description}</div>}</TableCell>
                  <TableCell className="text-sm">{g.bundle_names.length ? g.bundle_names.join(', ') : <span className="text-muted-foreground">空</span>}</TableCell>
                  <TableCell className="tabular-nums">{g.assigned_to}</TableCell>
                  <TableCell className="text-right">{manage && <><Button variant="ghost" size="sm" onClick={() => setEditing(g)}>编辑内容</Button><Button variant="ghost" size="sm" onClick={() => archive(g)}>{g.archived ? '恢复' : '归档'}</Button></>}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
      {creating && <CreateGroup onClose={() => { setCreating(false); reload() }} />}
      {editing && <EditGroupBundles group={editing} onClose={() => { setEditing(null); reload() }} />}
    </>
  )
}

function CreateGroup({ onClose }: { onClose: () => void }) {
  const [key, setKey] = useState('')
  const [name, setName] = useState('')
  const [desc, setDesc] = useState('')
  const [err, setErr] = useState('')
  async function submit() {
    try {
      await api.createGroup(key, name, desc)
      toast.success('已创建')
      onClose()
    } catch (e) { setErr((e as Error).message) }
  }
  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent>
        <DialogHeader><DialogTitle>新建权限组</DialogTitle><DialogDescription>key 会成为 teamai 端的 role 命名空间，建好后不能改。</DialogDescription></DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1"><Label htmlFor="g-key">key</Label><Input id="g-key" value={key} onChange={(e) => setKey(e.target.value)} placeholder="backend-pack" /></div>
          <div className="space-y-1"><Label htmlFor="g-name">名称</Label><Input id="g-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="后端工具包" /></div>
          <div className="space-y-1"><Label htmlFor="g-desc">描述</Label><Input id="g-desc" value={desc} onChange={(e) => setDesc(e.target.value)} /></div>
        </div>
        <Err msg={err} />
        <DialogFooter><Button variant="outline" onClick={onClose}>取消</Button><Button onClick={submit} disabled={!key || !name}>创建</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function EditGroupBundles({ group, onClose }: { group: Group; onClose: () => void }) {
  const resources = useAsync(() => api.resources())
  const [selected, setSelected] = useState<Set<string>>(new Set(group.bundle_ids))
  const [err, setErr] = useState('')
  function toggle(id: string) {
    setSelected((s) => { const n = new Set(s); n.has(id) ? n.delete(id) : n.add(id); return n })
  }
  async function save() {
    try {
      await api.setGroupBundles(group.id, Array.from(selected))
      toast.success('已保存')
      onClose()
    } catch (e) { setErr((e as Error).message) }
  }
  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent className="max-w-2xl">
        <DialogHeader><DialogTitle>{group.name} 的内容</DialogTitle><DialogDescription>勾选的资源只经这个权限组分发，不再随层级自动下发。</DialogDescription></DialogHeader>
        <div className="max-h-[50vh] overflow-auto rounded-md border">
          {resources.loading ? <TableSkeleton rows={3} /> : (resources.data?.items ?? []).filter((r) => !r.deleted && r.kind !== 'learning').map((r) => (
            <label key={r.id} className="flex cursor-pointer items-center gap-3 border-b px-3 py-2 text-sm last:border-0 hover:bg-muted">
              <input type="checkbox" checked={selected.has(r.id)} onChange={() => toggle(r.id)} />
              <Badge variant="secondary">{r.kind}</Badge>
              <span className="font-medium">{r.name}</span>
              <span className="text-xs text-muted-foreground">{r.level === 'org' ? '组织' : r.namespace}</span>
            </label>
          ))}
        </div>
        <Err msg={err} />
        <DialogFooter><Button variant="outline" onClick={onClose}>取消</Button><Button onClick={save}>保存</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
