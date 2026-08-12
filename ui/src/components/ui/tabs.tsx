"use client"

import * as React from "react"
import { cva } from "class-variance-authority"
import { Tabs as TabsPrimitive } from "radix-ui"
import type { VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"

function Tabs({
  className,
  orientation = "horizontal",
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Root>) {
  // font-sans is explicit because the app's html defaults to serif.
  // Without it the trigger labels inherit serif + body font-size, which
  // makes the shadcn pill/line variants look oversized.
  return (
    <TabsPrimitive.Root
      data-slot="tabs"
      data-orientation={orientation}
      className={cn(
        "group/tabs flex gap-2 font-sans data-horizontal:flex-col",
        className
      )}
      {...props}
    />
  )
}

const tabsListVariants = cva(
  "group/tabs-list inline-flex w-fit items-center justify-center rounded-lg p-[3px] text-muted-foreground group-data-horizontal/tabs:h-8 group-data-vertical/tabs:h-fit group-data-vertical/tabs:flex-col data-[variant=line]:rounded-none",
  {
    variants: {
      variant: {
        default: "bg-muted",
        line: "gap-1 bg-transparent",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

function TabsList({
  className,
  variant = "default",
  children,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.List> &
  VariantProps<typeof tabsListVariants>) {
  const listRef = React.useRef<HTMLDivElement>(null)
  // The line variant's underline is one shared element that slides
  // between triggers instead of a per-trigger underline that blinks.
  // Position is measured from the [data-active] trigger; a
  // MutationObserver keys the re-measure because Radix flips the
  // attribute outside React's knowledge.
  const [indicator, setIndicator] = React.useState<{
    left: number
    width: number
  } | null>(null)

  React.useLayoutEffect(() => {
    if (variant !== "line") return
    const list = listRef.current
    if (!list) return
    const update = () => {
      // Radix marks the active trigger with data-state="active"; the
      // data-active attribute only exists in Base UI builds. Match both.
      const active = list.querySelector<HTMLElement>(
        '[data-slot="tabs-trigger"][data-state="active"], [data-slot="tabs-trigger"][data-active]'
      )
      if (!active) {
        setIndicator(null)
        return
      }
      setIndicator({ left: active.offsetLeft, width: active.offsetWidth })
    }
    update()
    const mo = new MutationObserver(update)
    mo.observe(list, {
      subtree: true,
      attributes: true,
      attributeFilter: ["data-active", "data-state"],
    })
    const ro = new ResizeObserver(update)
    ro.observe(list)
    return () => {
      mo.disconnect()
      ro.disconnect()
    }
  }, [variant])

  return (
    <TabsPrimitive.List
      ref={listRef}
      data-slot="tabs-list"
      data-variant={variant}
      className={cn(
        tabsListVariants({ variant }),
        variant === "line" && "relative",
        className
      )}
      {...props}
    >
      {children}
      {variant === "line" && indicator && (
        <span
          aria-hidden
          className="absolute bottom-[-5px] left-0 h-0.5 bg-foreground group-data-vertical/tabs:hidden motion-reduce:transition-none"
          style={{
            width: indicator.width,
            transform: `translateX(${indicator.left}px)`,
            transition:
              "transform 180ms cubic-bezier(0.23, 1, 0.32, 1), width 180ms cubic-bezier(0.23, 1, 0.32, 1)",
          }}
        />
      )}
    </TabsPrimitive.List>
  )
}

function TabsTrigger({
  className,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Trigger>) {
  return (
    <TabsPrimitive.Trigger
      data-slot="tabs-trigger"
      className={cn(
        "relative inline-flex h-[calc(100%-1px)] flex-1 items-center justify-center gap-1.5 rounded-md border border-transparent px-1.5 py-0.5 text-xs font-medium whitespace-nowrap text-foreground/60 transition-colors group-data-vertical/tabs:w-full group-data-vertical/tabs:justify-start group-data-vertical/tabs:py-[calc(--spacing(1.25))] hover:text-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-1 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50 has-data-[icon=inline-end]:pr-1 has-data-[icon=inline-start]:pl-1 dark:text-muted-foreground dark:hover:text-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-3.5",
        "group-data-[variant=line]/tabs-list:bg-transparent group-data-[variant=line]/tabs-list:data-active:bg-transparent dark:group-data-[variant=line]/tabs-list:data-active:border-transparent dark:group-data-[variant=line]/tabs-list:data-active:bg-transparent",
        "data-active:bg-background data-active:text-foreground dark:data-active:border-input dark:data-active:bg-input/30 dark:data-active:text-foreground",
        // The horizontal line variant's underline is the sliding
        // indicator rendered by TabsList; the per-trigger after: only
        // survives for vertical lists, which the indicator doesn't cover.
        "after:absolute after:bg-foreground after:opacity-0 after:transition-opacity group-data-vertical/tabs:after:inset-y-0 group-data-vertical/tabs:after:-right-1 group-data-vertical/tabs:after:w-0.5 group-data-vertical/tabs:group-data-[variant=line]/tabs-list:data-active:after:opacity-100",
        className
      )}
      {...props}
    />
  )
}

function TabsContent({
  className,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Content>) {
  return (
    <TabsPrimitive.Content
      data-slot="tabs-content"
      className={cn("flex-1 text-xs/relaxed outline-none", className)}
      {...props}
    />
  )
}

export { Tabs, TabsList, TabsTrigger, TabsContent, tabsListVariants }
