import * as React from 'react';
import { cva, type VariantProps } from 'class-variance-authority';
import { cn } from '../../lib/utils';

const badgeVariants = cva(
  'inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold transition-colors focus:outline-none focus:ring-2 focus:ring-zinc-400 focus:ring-offset-2 focus:ring-offset-zinc-950',
  {
    variants: {
      variant: {
        default: 'border-transparent bg-zinc-800 text-zinc-100',
        secondary: 'border-transparent bg-zinc-700 text-zinc-200',
        success:
          'border-emerald-800/80 bg-emerald-950/80 text-emerald-300',
        warning:
          'border-amber-800/80 bg-amber-950/80 text-amber-200',
        destructive:
          'border-red-900/80 bg-red-950/80 text-red-200',
        outline: 'border-zinc-600 text-zinc-300',
      },
    },
    defaultVariants: {
      variant: 'default',
    },
  },
);

export interface BadgeProps
  extends React.HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return (
    <div className={cn(badgeVariants({ variant }), className)} {...props} />
  );
}

export { Badge, badgeVariants };
