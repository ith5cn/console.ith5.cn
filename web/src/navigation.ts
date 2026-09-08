import { Blocks, LayoutDashboard, Layers3, Route, ScanSearch, Users, Waypoints, type LucideIcon } from 'lucide-react'
export type Page = 'overview' | 'bundles' | 'groups' | 'assignments' | 'members' | 'audit' | 'explain'
export const NAV_ITEMS: { id: Page; label: string; icon: LucideIcon }[] = [
  { id: 'overview', label: '概览', icon: LayoutDashboard }, { id: 'bundles', label: '内容库', icon: Blocks },
  { id: 'groups', label: '权限组', icon: Layers3 }, { id: 'assignments', label: '授权策略', icon: Route },
  { id: 'members', label: '成员与设备', icon: Users }, { id: 'audit', label: '审计事件', icon: ScanSearch },
  { id: 'explain', label: '授权解释器', icon: Waypoints },
]
