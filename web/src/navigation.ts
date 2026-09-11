import {
  Activity, BookOpenText, Blocks, FolderKanban, GitPullRequest, HeartPulse, KeyRound, LayoutDashboard, Layers3,
  Route, Rocket, ScanSearch, Settings2, Users, UsersRound, type LucideIcon,
} from 'lucide-react'

export type Page =
  | 'overview' | 'resources' | 'changesets' | 'releases'
  | 'groups' | 'assignments'
  | 'teams' | 'projects' | 'members'
  | 'knowledge' | 'digest' | 'kb-health'
  | 'audit' | 'idp' | 'policy'

export interface NavItem { id: Page; label: string; icon: LucideIcon; perm?: string }
export interface NavGroup { label: string; items: NavItem[] }

// 分组对应后台的四类工作：内容、授权、组织、洞察与设置。
// perm 非空的项只对拥有该权限的人显示；服务端仍会再检查一次。
export const NAV_GROUPS: NavGroup[] = [
  { label: '内容', items: [
    { id: 'overview', label: '概览', icon: LayoutDashboard },
    { id: 'resources', label: '内容库', icon: Blocks },
    { id: 'changesets', label: '变更集', icon: GitPullRequest },
    { id: 'releases', label: '发布历史', icon: Rocket },
  ] },
  { label: '授权', items: [
    { id: 'groups', label: '权限组', icon: Layers3 },
    { id: 'assignments', label: '授权策略', icon: Route, perm: 'membership:manage' },
  ] },
  { label: '组织', items: [
    { id: 'teams', label: '团队', icon: UsersRound },
    { id: 'projects', label: '项目', icon: FolderKanban },
    { id: 'members', label: '成员与设备', icon: Users },
  ] },
  { label: '洞察', items: [
    { id: 'knowledge', label: '知识库', icon: BookOpenText },
    { id: 'digest', label: '周报', icon: Activity },
    { id: 'kb-health', label: '知识库健康', icon: HeartPulse },
    { id: 'audit', label: '审计', icon: ScanSearch, perm: 'audit:read' },
  ] },
  { label: '设置', items: [
    { id: 'idp', label: '身份源', icon: KeyRound, perm: 'identity:manage' },
    { id: 'policy', label: '组织策略', icon: Settings2, perm: 'organization:manage' },
  ] },
]

export const ALL_PAGES: Page[] = NAV_GROUPS.flatMap((g) => g.items.map((i) => i.id))

export function pageLabel(page: Page) {
  for (const g of NAV_GROUPS) {
    const item = g.items.find((i) => i.id === page)
    if (item) return item.label
  }
  return '控制台'
}

// 路由用 hash：服务端把任何非 /v1 路径都回 index.html，hash 不会打到服务端，
// 也不需要额外的路由库。形如 #/changesets/abc。
export function navigate(page: Page, id?: string) {
  location.hash = id ? `#/${page}/${id}` : `#/${page}`
}

export function parseHash(hash: string): { page: Page; id?: string } {
  const parts = hash.replace(/^#\/?/, '').split('/').filter(Boolean)
  const page = parts[0] as Page
  if (!ALL_PAGES.includes(page)) return { page: 'overview' }
  return { page, id: parts[1] }
}
