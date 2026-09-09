import { useEffect, useMemo } from 'react'
import { Background, Handle, Position, ReactFlow, useReactFlow, type Edge, type Node, type NodeProps } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import type { LucideIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

// 概览页的 ith5 工作流图：云端一条链（发布 → 组 → 授权 → 解析），
// 本机一条链（sync → 落盘 → hook → 审计），审计再回到内容迭代，形成闭环。
//
// 节点位置写死：链路是产品形态的一部分，不随数据变化，自动布局只会让它每次
// 长得不一样。变化的只有每个节点上的实时数字与状态。
export type StepStatus = 'ok' | 'warn' | 'idle'
export type Step = {
  id: string; label: string; note: string; icon: LucideIcon
  side: 'cloud' | 'local'; status?: StepStatus; loading?: boolean
}

type StepNode = Node<{ step: Step }, 'step'>

const STATUS_STYLE: Record<StepStatus, string> = {
  ok: 'text-success', warn: 'text-warning', idle: 'text-muted-foreground',
}

function StepCard({ data }: NodeProps<StepNode>) {
  const { step } = data
  const { icon: Icon } = step
  const cloud = step.side === 'cloud'
  return (
    <div className={cn('w-40 rounded-xl border bg-card px-3 py-2.5 shadow-sm', cloud ? 'border-primary/25' : 'border-emerald-500/25')}>
      <Handle type="target" position={Position.Left} className="!size-1.5 !border-0 !bg-border" />
      <div className="flex items-center gap-2">
        <span className={cn('grid size-7 shrink-0 place-items-center rounded-lg', cloud ? 'bg-accent text-accent-foreground' : 'bg-emerald-50 text-success')}><Icon className="size-4" /></span>
        <strong className="truncate text-xs font-medium">{step.label}</strong>
        {step.status && <span className={cn('ml-auto size-1.5 shrink-0 rounded-full bg-current', STATUS_STYLE[step.status])} />}
      </div>
      <div className={cn('mt-1.5 text-[11px] leading-4', step.loading ? 'text-muted-foreground/60' : 'text-muted-foreground')}>{step.loading ? '读取中…' : step.note}</div>
      <Handle type="source" position={Position.Right} className="!size-1.5 !border-0 !bg-border" />
    </div>
  )
}

const nodeTypes = { step: StepCard }

// 云端一行在上，本机一行在下并反向排布，最后一个节点正好回到起点上方，
// 反馈边不必横穿整张图。
const CLOUD_X = [0, 190, 380, 570]
const LOCAL_X = [570, 380, 190, 0]
const LOCAL_Y = 150

const line = (id: string, source: string, target: string, extra?: Partial<Edge>): Edge => ({
  id, source, target, type: 'smoothstep', animated: true,
  style: { stroke: 'var(--color-primary)', strokeWidth: 1.5 }, ...extra,
})

// fitView 只在初始化时跑一次，窗口变窄后图会被裁掉，所以自己补一次重排。
function FitOnResize() {
  const { fitView } = useReactFlow()
  useEffect(() => {
    const refit = () => { void fitView({ padding: 0.14 }) }
    window.addEventListener('resize', refit)
    return () => window.removeEventListener('resize', refit)
  }, [fitView])
  return null
}

export function WorkflowFlow({ steps }: { steps: Step[] }) {
  const nodes = useMemo<StepNode[]>(() => {
    const cloud = steps.filter((s) => s.side === 'cloud')
    const local = steps.filter((s) => s.side === 'local')
    const place = (list: Step[], xs: number[], y: number): StepNode[] => list.map((step, i) => ({
      id: step.id, type: 'step', position: { x: xs[i] ?? i * 190, y }, data: { step },
      draggable: false, selectable: false, connectable: false,
    }))
    return [...place(cloud, CLOUD_X, 0), ...place(local, LOCAL_X, LOCAL_Y)]
  }, [steps])

  const edges = useMemo<Edge[]>(() => {
    const cloud = steps.filter((s) => s.side === 'cloud')
    const local = steps.filter((s) => s.side === 'local')
    const chain = (list: Step[], tag: string) => list.slice(1).map((s, i) => line(`${tag}-${i}`, list[i].id, s.id))
    const out: Edge[] = [...chain(cloud, 'cloud'), ...chain(local, 'local')]
    if (cloud.length && local.length) {
      // 云端末端 → 本机起点：这一跳就是 `ith5 sync`，标出来。
      out.push(line('handoff', cloud[cloud.length - 1].id, local[0].id, { label: 'sync', labelStyle: { fontSize: 10, fill: 'var(--color-muted-foreground)' }, labelBgStyle: { fill: 'var(--color-card)' } }))
      // 审计回流到内容迭代：虚线、不流动，表示这是人的动作而非自动链路。
      out.push(line('feedback', local[local.length - 1].id, cloud[0].id, {
        animated: false, label: '反馈迭代',
        labelStyle: { fontSize: 10, fill: 'var(--color-muted-foreground)' }, labelBgStyle: { fill: 'var(--color-card)' },
        style: { stroke: 'var(--color-border)', strokeWidth: 1.5, strokeDasharray: '4 4' },
      }))
    }
    return out
  }, [steps])

  return (
    <div className="h-72 w-full" aria-label="ith5 分发工作流">
      <ReactFlow
        nodes={nodes} edges={edges} nodeTypes={nodeTypes}
        fitView fitViewOptions={{ padding: 0.14 }}
        proOptions={{ hideAttribution: false }}
        nodesDraggable={false} nodesConnectable={false} elementsSelectable={false}
        panOnDrag={false} panOnScroll={false} zoomOnScroll={false} zoomOnPinch={false} zoomOnDoubleClick={false}
        preventScrolling={false}
      >
        <Background gap={18} size={1} color="var(--color-border)" />
        <FitOnResize />
      </ReactFlow>
    </div>
  )
}
