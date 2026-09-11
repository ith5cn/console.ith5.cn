import { Blocks, BookOpenText, GitPullRequest, Users } from 'lucide-react'
import { api, getMe } from '@/api'
import { Card, CardContent } from '@/components/ui/card'
import { navigate } from '@/navigation'
import { Err, fmtTime, PageHeader, TableSkeleton, useAsync } from '@/ui'

function Stat({ icon: Icon, label, value, onClick }: { icon: typeof Blocks; label: string; value: string | number; onClick?: () => void }) {
  return (
    <Card className={onClick ? 'cursor-pointer transition-colors hover:bg-muted/50' : ''} onClick={onClick}>
      <CardContent className="flex items-center gap-3 p-4">
        <span className="grid size-10 place-items-center rounded-lg bg-accent text-accent-foreground"><Icon className="size-5" /></span>
        <div><div className="text-xl font-semibold tabular-nums">{value}</div><div className="text-xs text-muted-foreground">{label}</div></div>
      </CardContent>
    </Card>
  )
}

export function Overview() {
  const me = getMe()!
  const resources = useAsync(() => api.resources())
  const review = useAsync(() => api.changesets('review'))
  const members = useAsync(() => api.members())
  const learnings = useAsync(() => api.learnings())
  const digest = useAsync(() => api.digest())

  const loading = resources.loading || review.loading || members.loading || learnings.loading
  const err = resources.err || review.err || members.err || learnings.err

  return (
    <>
      <PageHeader title={`${me.membership.org_name}`} description="团队 AI 编码配置的控制面：内容按层级与权限组下发，改动经变更集审核与发布，经验在知识库沉淀。" />
      <Err msg={err} />
      {loading ? <TableSkeleton /> : (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <Stat icon={Blocks} label="已发布资源" value={resources.data?.items.filter((r) => !r.deleted).length ?? 0} onClick={() => navigate('resources')} />
          <Stat icon={GitPullRequest} label="待我审核" value={review.data?.items.length ?? 0} onClick={() => navigate('changesets', 'review')} />
          <Stat icon={Users} label="活跃成员" value={members.data?.items.filter((m) => m.status === 'active').length ?? 0} onClick={() => navigate('members')} />
          <Stat icon={BookOpenText} label="知识条目" value={learnings.data?.items.length ?? 0} onClick={() => navigate('knowledge')} />
        </div>
      )}
      {digest.data && (
        <Card className="mt-6">
          <CardContent className="p-5">
            <div className="mb-3 flex items-baseline justify-between"><h2 className="font-semibold">本周（{digest.data.week_start} 起）</h2><button className="text-sm text-primary underline-offset-4 hover:underline" onClick={() => navigate('digest')}>查看周报</button></div>
            <dl className="grid gap-4 text-sm sm:grid-cols-4">
              <div><dt className="text-muted-foreground">会话</dt><dd className="text-lg font-semibold tabular-nums">{digest.data.sessions}</dd></div>
              <div><dt className="text-muted-foreground">对话轮次</dt><dd className="text-lg font-semibold tabular-nums">{digest.data.prompt_turns}</dd></div>
              <div><dt className="text-muted-foreground">活跃成员</dt><dd className="text-lg font-semibold tabular-nums">{digest.data.active_members}</dd></div>
              <div><dt className="text-muted-foreground">估算成本</dt><dd className="text-lg font-semibold tabular-nums">${(digest.data.cost_micros / 1e6).toFixed(2)}</dd></div>
            </dl>
          </CardContent>
        </Card>
      )}
      {review.data && review.data.items.length > 0 && (
        <Card className="mt-6">
          <CardContent className="p-5">
            <h2 className="mb-3 font-semibold">待你审核</h2>
            <ul className="divide-y text-sm">
              {review.data.items.slice(0, 5).map((cs) => (
                <li key={cs.id} className="flex items-center gap-3 py-2">
                  <button className="min-w-0 flex-1 truncate text-left hover:underline" onClick={() => navigate('changesets', cs.id)}>{cs.title}</button>
                  <span className="text-muted-foreground">{cs.author_email}</span>
                  <span className="text-xs text-muted-foreground">{fmtTime(cs.updated_at)}</span>
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      )}
    </>
  )
}
