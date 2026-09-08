import * as React from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

const buttonVariants = cva('focus-ring inline-flex shrink-0 items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-medium transition-colors duration-200 disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-4 [&_svg]:shrink-0', {
  variants: {
    variant: {
      default: 'bg-primary text-primary-foreground shadow-glow hover:bg-primary/90',
      outline: 'border bg-card text-foreground hover:bg-muted', secondary: 'bg-secondary text-secondary-foreground hover:bg-secondary/80',
      ghost: 'text-muted-foreground hover:bg-muted hover:text-foreground', destructive: 'bg-destructive text-white hover:bg-destructive/90', link: 'text-primary underline-offset-4 hover:underline',
    },
    size: { default: 'h-9 px-4', sm: 'h-8 px-3 text-xs', lg: 'h-10 px-5', icon: 'size-9' },
  },
  defaultVariants: { variant: 'default', size: 'default' },
})

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {}
export function Button({ className, variant, size, ...props }: ButtonProps) { return <button className={cn(buttonVariants({ variant, size }), className)} {...props} /> }
export { buttonVariants }
