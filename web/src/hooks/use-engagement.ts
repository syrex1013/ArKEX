"use client";

import { useCallback, useEffect, useState } from "react";

import { api } from "@/lib/api";
import type { EngagementWithStats } from "@/lib/types";
import { setVisibleInterval } from "@/lib/polling";

// Shared engagement selection: localStorage key "artex_engagement" holds the current
// engagement id as a string ("0" or empty = all engagements). Every open tab/page
// syncs via the "artex-engagement-changed" window event, so pages re-filter live.
export const ENGAGEMENT_STORAGE_KEY = "artex_engagement";
const ENGAGEMENT_CHANGE_EVENT = "artex-engagement-changed";

function readSelectedId(): string {
  if (typeof window === "undefined") return "0";
  return localStorage.getItem(ENGAGEMENT_STORAGE_KEY) || "0";
}

export function useEngagementSelection(): [string, (id: string) => void] {
  const [selectedId, setSelectedId] = useState<string>("0");

  useEffect(() => {
    setSelectedId(readSelectedId());
    const sync = () => setSelectedId(readSelectedId());
    window.addEventListener(ENGAGEMENT_CHANGE_EVENT, sync);
    window.addEventListener("storage", sync);
    return () => {
      window.removeEventListener(ENGAGEMENT_CHANGE_EVENT, sync);
      window.removeEventListener("storage", sync);
    };
  }, []);

  const select = useCallback((id: string) => {
    localStorage.setItem(ENGAGEMENT_STORAGE_KEY, id);
    window.dispatchEvent(new Event(ENGAGEMENT_CHANGE_EVENT));
    setSelectedId(id);
  }, []);

  return [selectedId, select];
}

export function useEngagements() {
  const [engagements, setEngagements] = useState<EngagementWithStats[]>([]);

  const reload = useCallback(async () => {
    try {
      const r = await api.engagements.list();
      setEngagements(r.engagements ?? []);
    } catch {
      // Keep the last known list; the sidebar switcher degrades to "All engagements".
    }
  }, []);

  useEffect(() => {
    void reload();
    return setVisibleInterval(reload, 30_000);
  }, [reload]);

  return { engagements, reload };
}
