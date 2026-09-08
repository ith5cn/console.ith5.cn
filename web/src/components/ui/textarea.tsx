import * as React from 'react'
import { cn } from '@/lib/utils'
export function Textarea({ className, ...props }: React.TextareaHTMLAttributes<HTMLTextAreaElement>) { return <textarea className={cn('focus-ring flex min-h-24 w-full rounded-md border border-input bg-card px-3 py-2 text-sm placeholder:text-muted-foreground disabled:opacity-50', className)} {...props} /> }
