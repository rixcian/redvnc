import * as React from 'react';
import * as SheetPrimitive from '@radix-ui/react-dialog';
import { cn } from '../../lib/utils';

const Sheet = SheetPrimitive.Root;

const SheetTrigger = SheetPrimitive.Trigger;

const SheetClose = SheetPrimitive.Close;

const SheetPortal = SheetPrimitive.Portal;

const SheetOverlay = React.forwardRef<
  React.ElementRef<typeof SheetPrimitive.Overlay>,
  React.ComponentPropsWithoutRef<typeof SheetPrimitive.Overlay>
>(({ className, ...props }, ref) => (
  <SheetPrimitive.Overlay
    className={cn(
      'fixed inset-0 z-[1000] bg-black/40 transition-opacity duration-300',
      'data-[state=closed]:opacity-0 data-[state=open]:opacity-100',
      className,
    )}
    {...props}
    ref={ref}
  />
));
SheetOverlay.displayName = SheetPrimitive.Overlay.displayName;

interface SheetContentProps
  extends React.ComponentPropsWithoutRef<typeof SheetPrimitive.Content> {
  hideOverlay?: boolean;
  /** When true, omit Radix translate transitions so Framer Motion (or similar) can drive slide. */
  disableBuiltInSlide?: boolean;
}

const SheetContent = React.forwardRef<
  React.ElementRef<typeof SheetPrimitive.Content>,
  SheetContentProps
>(({ className, children, hideOverlay, disableBuiltInSlide, style, forceMount, ...props }, ref) => (
  // Portal must forceMount when Content does, or Radix unmounts the portal subtree on close
  // before Framer (or CSS) exit animations can run.
  <SheetPortal forceMount={forceMount}>
    {!hideOverlay ? <SheetOverlay /> : null}
    <SheetPrimitive.Content
      ref={ref}
      forceMount={forceMount}
      style={style}
      className={cn(
        'fixed right-0 z-[1001] flex flex-col border-l border-zinc-800 bg-zinc-900 shadow-2xl',
        !disableBuiltInSlide && [
          'duration-300 ease-out',
          'data-[state=closed]:translate-x-full data-[state=open]:translate-x-0',
          'data-[state=open]:transition-transform data-[state=closed]:transition-transform',
        ],
        className,
      )}
      {...props}
    >
      {children}
    </SheetPrimitive.Content>
  </SheetPortal>
));
SheetContent.displayName = SheetPrimitive.Content.displayName;

export {
  Sheet,
  SheetPortal,
  SheetOverlay,
  SheetTrigger,
  SheetClose,
  SheetContent,
};
