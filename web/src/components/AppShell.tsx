import { useState, type ReactNode } from 'react'
import { ChevronDown, Eye, LogOut, Menu, Terminal } from 'lucide-react'
import { can, type Me } from '@/api'
import { NAV_GROUPS, navigate, pageLabel, type Page } from '@/navigation'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

function Nav({ page, compact = false, onNavigate }: { page: Page; compact?: boolean; onNavigate?: () => void }) {
  return (
    <nav className="grid gap-4" aria-label="主要导航">
      {NAV_GROUPS.map((group) => {
        const items = group.items.filter((item) => !item.perm || can(item.perm))
        if (items.length === 0) return null
        return (
          <div key={group.label} className="grid gap-1">
            {!compact && <div className="px-3 text-[10px] font-medium uppercase tracking-[.15em] text-muted-foreground">{group.label}</div>}
            {items.map(({ id, label, icon: Icon }) => {
              const active = page === id
              const button = (
                <button
                  key={id}
                  type="button"
                  aria-current={active ? 'page' : undefined}
                  onClick={() => { navigate(id); onNavigate?.() }}
                  className={cn(
                    'focus-ring flex h-9 w-full items-center gap-3 rounded-md px-3 text-left text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground',
                    active && 'bg-accent text-accent-foreground',
                    compact && 'justify-center px-0',
                  )}
                >
                  <Icon className="size-4 shrink-0" />
                  <span className={compact ? 'sr-only' : ''}>{label}</span>
                </button>
              )
              return compact
                ? <Tooltip key={id}><TooltipTrigger asChild>{button}</TooltipTrigger><TooltipContent side="right">{label}</TooltipContent></Tooltip>
                : button
            })}
          </div>
        )
      })}
    </nav>
  )
}

function UserMenu({ me, onLogout, compact }: { me: Me; onLogout: () => void; compact?: boolean }) {
  const initial = me.account.email.slice(0, 1).toUpperCase()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button className={cn('focus-ring flex items-center gap-2 rounded-md p-2 text-left hover:bg-muted', !compact && 'mt-3 w-full')}>
          <span className="grid size-8 shrink-0 place-items-center rounded-full bg-slate-700 text-xs font-medium text-white">{initial}</span>
          {!compact && (
            <span className="hidden min-w-0 flex-1 lg:block">
              <span className="block truncate text-xs font-medium">{me.account.email}</span>
              <span className="block text-[11px] text-muted-foreground">{me.membership.org_name} · {me.membership.role}</span>
            </span>
          )}
          {!compact && <ChevronDown className="hidden size-3.5 text-muted-foreground lg:block" />}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent side={compact ? 'bottom' : 'top'} align={compact ? 'end' : 'start'}>
        <DropdownMenuLabel>{me.account.email}</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={onLogout}><LogOut />退出登录</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function AppShell({ me, page, onLogout, children }: { me: Me; page: Page; onLogout: () => void; children: ReactNode }) {
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  const viewer = me.membership.role === 'viewer'
  return (
    <TooltipProvider delayDuration={250}>
      <div className="min-h-screen md:grid md:grid-cols-[72px_minmax(0,1fr)] lg:grid-cols-[232px_minmax(0,1fr)]">
        <aside className="hidden min-h-screen flex-col border-r bg-card p-3 md:flex lg:p-4">
          <div className="mb-6 flex h-9 items-center justify-center gap-2 lg:justify-start lg:px-2">
            <span className="grid size-8 place-items-center rounded-lg bg-primary text-white"><Terminal className="size-4" /></span>
            <span className="hidden font-semibold lg:block">ITH5</span>
            <span className="ml-auto hidden font-mono text-[10px] text-muted-foreground lg:block">{me.membership.org_slug}</span>
          </div>
          <div className="hidden lg:block"><Nav page={page} /></div>
          <div className="lg:hidden"><Nav page={page} compact /></div>
          <div className="mt-auto"><UserMenu me={me} onLogout={onLogout} /></div>
        </aside>
        <div className="min-w-0">
          <header className="sticky top-0 z-30 flex h-16 items-center border-b bg-background/95 px-4 backdrop-blur sm:px-6 lg:px-8">
            <Sheet open={mobileNavOpen} onOpenChange={setMobileNavOpen}>
              <SheetTrigger asChild><Button variant="outline" size="icon" className="mr-3 md:hidden" aria-label="打开导航"><Menu /></Button></SheetTrigger>
              <SheetContent side="left" className="w-72">
                <SheetTitle className="mb-6 flex items-center gap-2"><span className="grid size-8 place-items-center rounded-lg bg-primary text-white"><Terminal className="size-4" /></span>ITH5</SheetTitle>
                <Nav page={page} onNavigate={() => setMobileNavOpen(false)} />
              </SheetContent>
            </Sheet>
            <span className="text-muted-foreground">{me.membership.org_name}&nbsp; / &nbsp;<strong className="font-medium text-foreground">{pageLabel(page)}</strong></span>
            {viewer && <span className="ml-auto hidden items-center gap-1.5 rounded-full bg-amber-50 px-3 py-1 text-xs font-medium text-amber-700 sm:flex"><Eye className="size-3.5" />只读账号</span>}
            <div className={viewer ? 'ml-2 md:hidden' : 'ml-auto md:hidden'}><UserMenu me={me} onLogout={onLogout} compact /></div>
          </header>
          {viewer && <div className="flex items-center justify-center gap-2 border-b border-amber-200 bg-amber-50 px-4 py-2 text-sm text-amber-800"><Eye className="size-4" />你可以浏览所有页面，但无法修改数据。</div>}
          <main className="mx-auto w-full max-w-[1440px] p-4 sm:p-6 lg:p-8">{children}</main>
        </div>
      </div>
    </TooltipProvider>
  )
}
