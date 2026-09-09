import { Check, Copy, Terminal } from 'lucide-react'
import { toast } from 'sonner'
import { useState } from 'react'
import { getOrgSlug, getUser } from '@/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

// 概览页的「装到本机」卡片：新人照着敲四条命令就能拿到公司分发的内容。
//
// 服务端地址固定用线上地址，不取当前页面的 origin：这些命令是给人抄到
// 自己机器上执行的，从 localhost 的后台复制出来的 localhost 命令，
// 换台机器就是错的。自建实例改这一个常量即可。
const SERVER = 'https://console.ith5.cn'
const CLI_DOCS: { cmd: string; desc: string }[] = [
  { cmd: 'ith5 status', desc: '查看已装内容与同步状态' },
  { cmd: 'ith5 doctor', desc: '逐项体检，给出可执行建议' },
  { cmd: 'ith5 watch', desc: '本机工作流看板' },
  { cmd: 'ith5 logout --purge', desc: '登出并移除已安装内容' },
]

// 公开演示环境的只读账号。README 里也是这一份，改口令时两处一起改。
const DEMO = { org: 'demo', email: 'test@ith5.cn', password: 'xLzhQrXMPs6JTEcgignI0p0j' }

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}

function CopyButton({ value, label }: { value: string; label: string }) {
  const [done, setDone] = useState(false)
  return <Button variant="ghost" size="icon" aria-label={`复制${label}`} className="size-7 shrink-0 text-slate-400 hover:text-slate-100" onClick={async () => {
    if (!await copy(value)) { toast.error('浏览器不允许写剪贴板，请手动选中复制'); return }
    setDone(true); setTimeout(() => setDone(false), 1500); toast.success('已复制')
  }}>{done ? <Check className="text-emerald-400" /> : <Copy />}</Button>
}

function Command({ step, title, cmd }: { step: number; title: string; cmd: string }) {
  return <li className="min-w-0">
    <div className="flex items-center gap-2 text-xs font-medium"><span className="grid size-5 shrink-0 place-items-center rounded-full bg-accent text-[11px] text-accent-foreground">{step}</span>{title}</div>
    <div className="mt-1.5 flex items-center gap-1 rounded-lg bg-slate-950 py-1.5 pl-3 pr-1.5">
      <code className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap font-mono text-xs leading-6 text-slate-300"><span className="mr-1.5 select-none text-primary">$</span>{cmd}</code>
      <CopyButton value={cmd} label={`命令：${cmd}`} />
    </div>
  </li>
}

function Field({ label, value, secret }: { label: string; value: string; secret?: boolean }) {
  return <div className="flex items-center gap-1 rounded-md border bg-card px-2.5 py-1">
    <span className="shrink-0 text-xs text-muted-foreground">{label}</span>
    <code className="min-w-0 flex-1 truncate font-mono text-xs">{value}</code>
    <Button variant="ghost" size="icon" aria-label={`复制${label}`} className="size-6 shrink-0" onClick={async () => { (await copy(value)) ? toast.success(`已复制${label}`) : toast.error('浏览器不允许写剪贴板，请手动选中复制') }}><Copy className="size-3.5" /></Button>
    {secret && <span className="sr-only">这是公开演示口令</span>}
  </div>
}

export function QuickStart() {
  const user = getUser()
  // viewer 是只读演示角色，看到这张卡的多半是来试用的访客，直接把演示口令给他；
  // 正式成员用自己的账号，屏幕上不该出现别人的口令。
  const demo = user?.role === 'viewer' || user?.email === DEMO.email
  const org = getOrgSlug() || (demo ? DEMO.org : '')
  return <Card>
    <CardHeader className="flex-row items-center border-b"><CardTitle className="flex items-center gap-2"><Terminal className="size-4 text-primary" />装到本机</CardTitle><span className="ml-auto text-xs text-muted-foreground">员工在自己机器上执行，全程约 6 秒</span></CardHeader>
    <CardContent className="p-5">
      <ol className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <Command step={1} title="安装 CLI" cmd={`curl -fsSL ${SERVER}/install.sh | sh`} />
        <Command step={2} title="登录并绑定本机" cmd={`ITH5_SERVER=${SERVER} ith5 login`} />
        <Command step={3} title="同步公司分发的内容" cmd="ith5 sync" />
        <Command step={4} title="验证" cmd="ith5 status" />
      </ol>
      <div className="mt-5 border-t pt-4">
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1"><strong className="text-xs font-medium">{demo ? '演示账号' : '登录信息'}</strong><span className="text-xs text-muted-foreground">{demo ? '登录时按提示依次输入；这是公开演示环境的只读账号，请勿放入真实数据' : '密码由管理员在「成员与设备」里设置或重置'}</span></div>
        <div className="mt-2 grid gap-2 sm:grid-cols-3">
          <Field label="组织" value={org || DEMO.org} />
          <Field label="邮箱" value={demo ? DEMO.email : user?.email ?? ''} />
          {demo ? <Field label="密码" value={DEMO.password} secret /> : <div className="flex items-center rounded-md border border-dashed px-2.5 py-1 text-xs text-muted-foreground">密码：你自己的登录密码</div>}
        </div>
      </div>
      <div className="mt-4 flex flex-wrap gap-x-5 gap-y-1 border-t pt-4 text-xs text-muted-foreground">
        {CLI_DOCS.map((c) => <span key={c.cmd}><code className="font-mono text-foreground">{c.cmd}</code> · {c.desc}</span>)}
      </div>
    </CardContent>
  </Card>
}
