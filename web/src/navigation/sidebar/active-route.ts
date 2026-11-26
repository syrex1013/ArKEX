import { type NavGroup, type NavMainItem, sidebarItems } from "@/navigation/sidebar/sidebar-items";

export interface ActiveRoute {
  group: string;
  title: string;
  url: string;
}

function normalize(pathname: string): string {
  const decoded = (() => {
    try {
      return decodeURIComponent(pathname);
    } catch {
      return pathname;
    }
  })();
  return decoded.replace(/\/+$/, "") || "/";
}

function matches(url: string, path: string): boolean {
  return path === url || path.startsWith(`${url}/`);
}

/** Longest-prefix match against the sidebar tree, so `/function/tasks/12` resolves to Tasks. */
export function resolveActiveRoute(pathname: string): ActiveRoute | null {
  const path = normalize(pathname);
  let best: ActiveRoute | null = null;
  let bestLength = -1;

  for (const group of sidebarItems as NavGroup[]) {
    for (const item of group.items as NavMainItem[]) {
      const candidates: { title: string; url: string }[] = [];
      if ("url" in item && item.url) candidates.push({ title: item.title, url: item.url });
      for (const sub of item.subItems ?? []) candidates.push({ title: sub.title, url: sub.url });

      for (const candidate of candidates) {
        if (!matches(candidate.url, path)) continue;
        if (candidate.url.length <= bestLength) continue;
        bestLength = candidate.url.length;
        best = { group: group.label ?? "", title: candidate.title, url: candidate.url };
      }
    }
  }

  return best;
}
