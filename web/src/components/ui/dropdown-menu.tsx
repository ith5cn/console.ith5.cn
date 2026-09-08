import * as React from 'react'
import { DropdownMenu as DropdownMenuPrimitive } from 'radix-ui'
import { cn } from '@/lib/utils'
export const DropdownMenu = DropdownMenuPrimitive.Root
export const DropdownMenuTrigger = DropdownMenuPrimitive.Trigger
export function DropdownMenuContent({ className, sideOffset = 6, ...props }: React.ComponentProps<typeof DropdownMenuPrimitive.Content>) { return <DropdownMenuPrimitive.Portal><DropdownMenuPrimitive.Content sideOffset={sideOffset} className={cn('z-[70] min-w-44 rounded-md border bg-popover p-1 text-popover-foreground shadow-xl', className)} {...props} /></DropdownMenuPrimitive.Portal> }
export function DropdownMenuItem({ className, ...props }: React.ComponentProps<typeof DropdownMenuPrimitive.Item>) { return <DropdownMenuPrimitive.Item className={cn('flex cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-2 text-sm outline-none focus:bg-muted data-[disabled]:opacity-50 [&_svg]:size-4', className)} {...props} /> }
export function DropdownMenuLabel({ className, ...props }: React.ComponentProps<typeof DropdownMenuPrimitive.Label>) { return <DropdownMenuPrimitive.Label className={cn('px-2 py-1.5 text-xs text-muted-foreground', className)} {...props} /> }
export function DropdownMenuSeparator({ className, ...props }: React.ComponentProps<typeof DropdownMenuPrimitive.Separator>) { return <DropdownMenuPrimitive.Separator className={cn('-mx-1 my-1 h-px bg-border', className)} {...props} /> }
