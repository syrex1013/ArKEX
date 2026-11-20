"use client";

import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

/** ThreatVector signature L-brackets around hero panels. */
export function CornerFrame({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("relative", className)}>
      <span aria-hidden className="pointer-events-none absolute top-0 left-0 h-3 w-3 border-t-[1.5px] border-l-[1.5px] border-primary" />
      <span aria-hidden className="pointer-events-none absolute top-0 right-0 h-3 w-3 border-t-[1.5px] border-r-[1.5px] border-primary" />
      <span aria-hidden className="pointer-events-none absolute bottom-0 left-0 h-3 w-3 border-b-[1.5px] border-l-[1.5px] border-primary" />
      <span aria-hidden className="pointer-events-none absolute right-0 bottom-0 h-3 w-3 border-r-[1.5px] border-b-[1.5px] border-primary" />
      {children}
    </div>
  );
}
