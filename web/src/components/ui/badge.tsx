import * as React from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

const badgeVariants = cva('inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium', {
  variants: { variant: { default: 'bg-muted text-muted-foreground', accent: 'bg-accent text-accent-foreground', success: 'bg-emerald-50 text-success', warning: 'bg-amber-50 text-warning', destructive: 'bg-red-50 text-destructive', outline: 'border bg-transparent text-muted-foreground' } },
  defaultVariants: { variant: 'default' },
})
export function Badge({ className, variant, ...props }: React.HTMLAttributes<HTMLSpanElement> & VariantProps<typeof badgeVariants>) { return <span className={cn(badgeVariants({ variant }), className)} {...props} /> }
