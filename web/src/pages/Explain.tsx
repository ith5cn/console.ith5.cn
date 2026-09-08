import { useState } from 'react'
import { ChevronRight, UserRoundCheck } from 'lucide-react'
import { api } from '../api'
import { Badge } from '@/components/ui/badge'
import { Card } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Empty, Err, PageHeader, TableSkeleton, Via, useAsync } from '../ui'

export function Explain() {
  const members = useAsync(() => api.listMembers()); const [uid, setUid] = useState(''); const result = useAsync(() => uid ? api.explain(uid) : Promise.resolve(null), [uid])
  return <><PageHeader title="授权解释器" description="回答“为什么他能拿到这个”，以及“为什么没有”。" /><Card className="mb-4 p-4"><div className="flex flex-col gap-3 sm:flex-row sm:items-end"><div className="w-full max-w-md space-y-2"><Label>查看成员</Label><Select value={uid} onValueChange={setUid}><SelectTrigger aria-label="选择成员"><SelectValue placeholder="请选择成员…" /></SelectTrigger><SelectContent>{members.data?.members.map((m) => <SelectItem key={m.id} value={m.id}>{m.name ? `${m.name} · ` : ''}{m.email}</SelectItem>)}</SelectContent></Select></div>{uid && result.data && !result.data.suspended && <Badge variant="success" className="mb-2"><UserRoundCheck className="size-3" />账号正常</Badge>}</div><Err msg={members.err} /></Card><Err msg={result.err} />{!uid ? <Empty>选择一个成员以解析当前授权</Empty> : result.loading ? <TableSkeleton rows={3} /> : result.data?.suspended ? <Empty>该成员已停用，同步时会获得空清单并清理本机内容</Empty> : !result.data?.grants.length ? <Empty>该成员目前拿不到任何内容</Empty> : <Card className="overflow-hidden"><Table><TableHeader><TableRow><TableHead>可获得内容</TableHead><TableHead>类型</TableHead><TableHead>版本</TableHead><TableHead>授权来源</TableHead><TableHead>解析路径</TableHead></TableRow></TableHeader><TableBody>{result.data.grants.map((g) => <TableRow key={g.bundle_id}><TableCell className="font-medium">{g.bundle_name}</TableCell><TableCell><Badge>{g.kind}</Badge></TableCell><TableCell className="font-mono">v{g.version}</TableCell><TableCell><Via via={g.via} /></TableCell><TableCell><span className="flex items-center gap-1.5 text-xs text-muted-foreground">成员 <ChevronRight className="size-3" /> {g.via[0]?.group_name ? '权限组' : '直接授权'} <ChevronRight className="size-3" /> 内容</span></TableCell></TableRow>)}</TableBody></Table></Card>}</>
}
