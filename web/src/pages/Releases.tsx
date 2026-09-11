import { useState } from 'react'
import { Undo2 } from 'lucide-react'
import { toast } from 'sonner'
import { api, can } from '@/api'
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { navigate } from '@/navigation'
import { Empty, Err, fmtTime, PageHeader, TableSkeleton, useAsync } from '@/ui'

export function Releases({ id }: { id?: string }) {
  const projects = useAsync(() => api.projects())
  const [scope, setScope] = useState('org')
  const list = useAsync(() => api.releases(scope === 'org' ? { level: 'org' } : scope === 'all' ? {} : { project_id: scope }), [scope])

  async function rollback(releaseId: string) {
    try {
      const cs = await api.rollback(releaseId, false)
      toast.success('已生成回滚变更集，提交审核后发布')
      navigate('changesets', cs.id)
    } catch (e) {
      toast.error((e as Error).message)
    }
  }

  return (
    <>
      <PageHeader title="发布历史" description="每次发布是不可变的。回滚不会改写历史，而是生成一个指向旧版本的新变更集。" />
      <div className="mb-4">
        <Select value={scope} onValueChange={setScope}>
          <SelectTrigger className="w-64"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="org">组织级</SelectItem>
            {can('audit:read') && <SelectItem value="all">全部落点</SelectItem>}
            {projects.data?.items.map((p) => <SelectItem key={p.id} value={p.id}>项目 · {p.name}</SelectItem>)}
          </SelectContent>
        </Select>
      </div>
      <Err msg={list.err} />
      {list.loading ? <TableSkeleton /> : !list.data?.items.length ? <Empty>这个落点还没有发布记录。</Empty> : (
        <Card className="overflow-hidden">
          <Table>
            <TableHeader><TableRow><TableHead>时间</TableHead><TableHead>发布者</TableHead><TableHead>内容</TableHead><TableHead /></TableRow></TableHeader>
            <TableBody>
              {list.data.items.map((r) => (
                <TableRow key={r.id} className={r.id === id ? 'bg-accent/40' : ''}>
                  <TableCell className="whitespace-nowrap">{fmtTime(r.published_at)}</TableCell>
                  <TableCell>{r.publisher_email || '—'}</TableCell>
                  <TableCell className="text-sm">
                    <div className="flex flex-wrap gap-1">
                      {r.items.map((it) => <Badge key={it.bundle_id + it.version} variant={it.deleted ? 'destructive' : 'secondary'}>{it.kind}/{it.name} v{it.version}</Badge>)}
                    </div>
                    <button className="mt-1 text-xs text-muted-foreground hover:underline" onClick={() => navigate('changesets', r.changeset_id)}>查看变更集</button>
                  </TableCell>
                  <TableCell className="text-right">
                    {can('release:publish') && (
                      <AlertDialog>
                        <AlertDialogTrigger asChild><Button variant="ghost" size="sm"><Undo2 />回滚</Button></AlertDialogTrigger>
                        <AlertDialogContent>
                          <AlertDialogHeader><AlertDialogTitle>回滚这次发布？</AlertDialogTitle><AlertDialogDescription>会生成一个变更集，把这次发布的每项内容回到它之前那一版；之前不存在的会被删除。变更集仍需提交与发布。</AlertDialogDescription></AlertDialogHeader>
                          <AlertDialogFooter><AlertDialogCancel>取消</AlertDialogCancel><AlertDialogAction onClick={() => rollback(r.id)}>生成回滚变更集</AlertDialogAction></AlertDialogFooter>
                        </AlertDialogContent>
                      </AlertDialog>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
    </>
  )
}
