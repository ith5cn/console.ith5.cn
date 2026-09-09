import { useEffect, useMemo } from 'react'
import { Background, Handle, MarkerType, Position, ReactFlow, useReactFlow, type Edge, type Node, type NodeProps } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import type { LucideIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

export type StepStatus = 'ok' | 'warn' | 'idle'
export type Step = {
  id: string
  label: string
  note: string
  icon: LucideIcon
  side: 'cloud' | 'local'
  status?: StepStatus
  loading?: boolean
}

type FlowNodeData = {
  step: Step
  index: string
  command: string
  description: string
  tags: string[]
  size: 'hero' | 'compact'
  tone: 'cyan' | 'violet' | 'magenta' | 'amber' | 'emerald'
}
type FlowNode = Node<FlowNodeData, 'workflow'>

const TONE = {
  cyan: { border: 'border-cyan-300/25', icon: 'border-cyan-300/20 bg-cyan-300/10 text-cyan-300', command: 'text-cyan-300', glow: 'shadow-[0_18px_50px_rgba(34,211,238,.08)]' },
  violet: { border: 'border-violet-300/25', icon: 'border-violet-300/20 bg-violet-300/10 text-violet-300', command: 'text-violet-300', glow: 'shadow-[0_18px_50px_rgba(139,92,246,.10)]' },
  magenta: { border: 'border-fuchsia-300/25', icon: 'border-fuchsia-300/20 bg-fuchsia-300/10 text-fuchsia-300', command: 'text-fuchsia-300', glow: 'shadow-[0_18px_50px_rgba(217,70,239,.08)]' },
  amber: { border: 'border-amber-300/25', icon: 'border-amber-300/20 bg-amber-300/10 text-amber-300', command: 'text-amber-300', glow: 'shadow-[0_18px_50px_rgba(251,191,36,.07)]' },
  emerald: { border: 'border-emerald-300/25', icon: 'border-emerald-300/20 bg-emerald-300/10 text-emerald-300', command: 'text-emerald-300', glow: 'shadow-[0_18px_50px_rgba(52,211,153,.07)]' },
} as const

function StatusDot({ status = 'idle', loading }: Pick<Step, 'status' | 'loading'>) {
  const label = loading ? '读取中' : status === 'ok' ? '运行正常' : status === 'warn' ? '需要关注' : '等待数据'
  return <span title={label} aria-label={label} className={cn(
    'size-2 rounded-full ring-4 ring-white/5',
    loading && 'animate-pulse bg-slate-500',
    !loading && status === 'ok' && 'bg-emerald-300 shadow-[0_0_12px_rgba(110,231,183,.75)]',
    !loading && status === 'warn' && 'bg-amber-300 shadow-[0_0_12px_rgba(252,211,77,.75)]',
    !loading && status === 'idle' && 'bg-slate-500',
  )} />
}

function WorkflowNode({ data }: NodeProps<FlowNode>) {
  const { step, size, tone, index, command, description, tags } = data
  const { icon: Icon } = step
  const color = TONE[tone]
  const hero = size === 'hero'
  const isRouter = step.id === 'router'
  const isAgent = step.id === 'agents'
  const isDelivery = step.id === 'delivery'

  return <article className={cn(
    'group relative overflow-hidden rounded-2xl border bg-[#11172a]/95 text-slate-100 backdrop-blur-md transition-colors duration-300',
    hero ? 'h-[208px] w-[356px] p-5' : 'h-[180px] w-[216px] p-4',
    color.border, color.glow,
  )}>
    <div className="pointer-events-none absolute inset-x-8 top-0 h-px bg-gradient-to-r from-transparent via-white/30 to-transparent" />
    <Handle id="target-left" type="target" position={Position.Left} className="workflow-handle" />
    {isRouter && <Handle id="target-top" type="target" position={Position.Top} className="workflow-handle" />}
    {isAgent && <Handle id="target-feedback" type="target" position={Position.Right} className="workflow-handle workflow-handle-feedback" />}

    <div className="flex items-start">
      <span className={cn('grid shrink-0 place-items-center rounded-xl border', hero ? 'size-11' : 'size-9', color.icon)}>
        <Icon className={hero ? 'size-5' : 'size-4'} />
      </span>
      <span className="ml-auto flex items-center gap-2 font-mono text-[9px] uppercase tracking-[.16em] text-slate-500">
        <StatusDot status={step.status} loading={step.loading} />{index}
      </span>
    </div>

    <div className={hero ? 'mt-5' : 'mt-3'}>
      <div className={cn('font-mono text-[11px]', color.command)}>{command}</div>
      <h3 className={cn('mt-1 font-semibold tracking-[-.02em]', hero ? 'text-xl' : 'text-base')}>{step.label}</h3>
      <p className="mt-1 text-[11px] leading-4 text-slate-400">{step.loading ? '正在读取组织实时状态…' : description}</p>
      {!hero && <p className={cn('mt-1 truncate font-mono text-[9px]', color.command)}>{step.loading ? '同步中' : step.note}</p>}
    </div>

    <div className={cn('absolute inset-x-4 bottom-4 flex items-center gap-1.5', hero && 'inset-x-5')}>
      {tags.map((tag) => <span key={tag} className="rounded-md border border-white/10 bg-white/[.025] px-2 py-1 font-mono text-[9px] text-slate-400">{tag}</span>)}
      {hero && <span className="ml-auto max-w-[120px] truncate text-[9px] text-slate-500">{step.loading ? '同步中' : step.note}</span>}
    </div>

    {isAgent && <Handle id="source-bottom" type="source" position={Position.Bottom} className="workflow-handle" />}
    {!isAgent && <Handle id="source-right" type="source" position={Position.Right} className={cn('workflow-handle', isDelivery && 'workflow-handle-feedback')} />}
  </article>
}

const nodeTypes = { workflow: WorkflowNode }

const NODE_META: Record<string, Omit<FlowNodeData, 'step'>> = {
  bundles: { index: '01 / INITIALIZE', command: '/ith5:init', description: '建立项目上下文与工程规范', tags: ['项目检查', '环境就绪'], size: 'hero', tone: 'cyan' },
  groups: { index: '02 / SPECIFY', command: '/ith5:prd', description: '把想法转为可执行的开发规格', tags: ['PRD', 'SPEC', 'TASKS'], size: 'hero', tone: 'violet' },
  agents: { index: '03 / ORCHESTRATE', command: '/ith5:ai', description: '前端、后端、数据库与契约并行协作', tags: ['N1', 'N2', 'N3', 'N4'], size: 'hero', tone: 'magenta' },
  router: { index: 'ROUTER', command: 'risk.route()', description: '按任务复杂度分配策略', tags: ['低', '中', '高'], size: 'compact', tone: 'cyan' },
  review: { index: 'CODEX CR', command: 'review.gate()', description: '自审与跨模型双轮审查', tags: ['ALLOW', 'BLOCK'], size: 'compact', tone: 'amber' },
  qa: { index: 'RISK-BASED QA', command: 'qa.evaluate()', description: '按风险评分动态触发', tags: ['功能', '回归', '安全'], size: 'compact', tone: 'cyan' },
  memory: { index: 'MEMORY', command: 'memory.commit()', description: '沉淀经验，复用于后续任务', tags: ['经验', '规范', '上下文'], size: 'compact', tone: 'violet' },
  delivery: { index: 'DELIVERY', command: 'ship.release()', description: '从生成代码到可交付成果', tags: ['代码', '测试', '文档'], size: 'compact', tone: 'emerald' },
}

const POSITIONS: Record<string, { x: number; y: number }> = {
  bundles: { x: 0, y: 20 }, groups: { x: 406, y: 20 }, agents: { x: 812, y: 20 },
  router: { x: 0, y: 330 }, review: { x: 245, y: 330 }, qa: { x: 490, y: 330 },
  memory: { x: 735, y: 330 }, delivery: { x: 980, y: 330 },
}

const edge = (id: string, source: string, target: string, extra: Partial<Edge> = {}): Edge => ({
  id, source, target, type: 'smoothstep', animated: true,
  className: 'workflow-edge',
  markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: '#67d8ed' },
  style: { stroke: '#67d8ed', strokeWidth: 1.5 },
  ...extra,
})

function FitOnResize() {
  const { fitView } = useReactFlow()
  useEffect(() => {
    const refit = () => { void fitView({ padding: 0.08, maxZoom: 1 }) }
    window.addEventListener('resize', refit)
    return () => window.removeEventListener('resize', refit)
  }, [fitView])
  return null
}

export function WorkflowFlow({ steps }: { steps: Step[] }) {
  const nodes = useMemo<FlowNode[]>(() => steps.map((step) => ({
    id: step.id,
    type: 'workflow',
    position: POSITIONS[step.id],
    data: { step, ...NODE_META[step.id] },
    draggable: false,
    selectable: false,
    connectable: false,
  })), [steps])

  const edges = useMemo<Edge[]>(() => [
    edge('initialize-specify', 'bundles', 'groups', { sourceHandle: 'source-right', targetHandle: 'target-left' }),
    edge('specify-agents', 'groups', 'agents', { sourceHandle: 'source-right', targetHandle: 'target-left' }),
    edge('agents-router', 'agents', 'router', { sourceHandle: 'source-bottom', targetHandle: 'target-top' }),
    edge('router-review', 'router', 'review', { sourceHandle: 'source-right', targetHandle: 'target-left' }),
    edge('review-qa', 'review', 'qa', { sourceHandle: 'source-right', targetHandle: 'target-left' }),
    edge('qa-memory', 'qa', 'memory', { sourceHandle: 'source-right', targetHandle: 'target-left' }),
    edge('memory-delivery', 'memory', 'delivery', { sourceHandle: 'source-right', targetHandle: 'target-left' }),
    edge('feedback-loop', 'delivery', 'agents', {
      sourceHandle: 'source-right', targetHandle: 'target-feedback', animated: false,
      className: 'workflow-edge workflow-edge-feedback',
      label: 'BLOCK',
      labelStyle: { fontSize: 10, fill: '#d98aa9', fontFamily: 'var(--font-mono)' },
      labelBgStyle: { fill: '#0a0f1f', fillOpacity: 0.95 },
      markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: '#b56886' },
      style: { stroke: '#b56886', strokeWidth: 1.25, strokeDasharray: '5 6' },
    }),
  ], [])

  return <div className="workflow-flow relative h-[540px] w-full overflow-x-auto overflow-y-hidden" aria-label="ith5 AI 自动开发工作流">
    <div className="pointer-events-none absolute left-1/2 top-[294px] z-10 flex -translate-x-1/2 items-center gap-3 whitespace-nowrap font-mono text-[10px] uppercase tracking-[.18em] text-slate-500">
      <span className="h-px w-14 bg-gradient-to-r from-transparent to-violet-400/40" />
      执行内核 · 路由 / 审查 / 质量 / 记忆
      <span className="h-px w-14 bg-gradient-to-r from-violet-400/40 to-transparent" />
    </div>
    <div className="h-full min-w-[760px]">
      <ReactFlow
        nodes={nodes} edges={edges} nodeTypes={nodeTypes}
        fitView fitViewOptions={{ padding: 0.08, maxZoom: 1 }} minZoom={0.2} maxZoom={1}
        proOptions={{ hideAttribution: true }}
        nodesDraggable={false} nodesConnectable={false} elementsSelectable={false}
        panOnDrag={false} panOnScroll={false} zoomOnScroll={false} zoomOnPinch={false} zoomOnDoubleClick={false}
        preventScrolling={false}
      >
        <Background gap={28} size={1} color="rgba(124, 144, 190, .12)" />
        <FitOnResize />
      </ReactFlow>
    </div>
  </div>
}
