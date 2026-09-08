import * as React from 'react'
import { Dialog as SheetPrimitive } from 'radix-ui'
import { X } from 'lucide-react'
import { cn } from '@/lib/utils'

export const Sheet = SheetPrimitive.Root
export const SheetTrigger = SheetPrimitive.Trigger
export const SheetClose = SheetPrimitive.Close
export function SheetContent({ className, children, side = 'right', ...props }: React.ComponentProps<typeof SheetPrimitive.Content> & { side?: 'left' | 'right' }) { return <SheetPrimitive.Portal><SheetPrimitive.Overlay className="fixed inset-0 z-50 bg-slate-950/30" /><SheetPrimitive.Content className={cn('fixed inset-y-0 z-50 flex w-[min(90vw,420px)] flex-col overflow-y-auto border bg-card p-6 shadow-2xl', side === 'right' ? 'right-0 border-l' : 'left-0 border-r', className)} {...props}>{children}<SheetPrimitive.Close className="focus-ring absolute right-4 top-4 rounded-md p-1 text-muted-foreground hover:bg-muted" aria-label="关闭"><X className="size-4" /></SheetPrimitive.Close></SheetPrimitive.Content></SheetPrimitive.Portal> }
export function SheetHeader({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) { return <div className={cn('mb-5 flex flex-col gap-1.5', className)} {...props} /> }
export function SheetTitle({ className, ...props }: React.ComponentProps<typeof SheetPrimitive.Title>) { return <SheetPrimitive.Title className={cn('text-lg font-semibold', className)} {...props} /> }
export function SheetDescription({ className, ...props }: React.ComponentProps<typeof SheetPrimitive.Description>) { return <SheetPrimitive.Description className={cn('text-sm text-muted-foreground', className)} {...props} /> }
