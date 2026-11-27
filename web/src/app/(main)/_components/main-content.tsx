"use client";

import { type ReactNode, useEffect, useState } from "react";

import { usePathname } from "next/navigation";

import { ChevronRight, X } from "lucide-react";

import { ConnectionIndicator } from "@/components/arkex/connection-indicator";
import { useLiveLog } from "@/components/arkex/live-stream-provider";
import { SidebarTrigger } from "@/components/ui/sidebar";
import { useCurrentUser } from "@/hooks/use-current-user";
import { t } from "@/i18n";
import { api } from "@/lib/api";
import { getLocalStorageValue, setLocalStorageValue } from "@/lib/local-storage.client";
import { cn } from "@/lib/utils";
import { resolveActiveRoute } from "@/navigation/sidebar/active-route";

import { AccountSwitcher } from "./sidebar/account-switcher";
import { DirectEgressToggle } from "./sidebar/direct-egress-toggle";
import { LayoutControls } from "./sidebar/layout-controls";
import { MullvadIndicator } from "./sidebar/mullvad-indicator";
import { SearchDialog } from "./sidebar/search-dialog";
import { ThemeSwitcher } from "./sidebar/theme-switcher";

// Task detail pages bring their own navigation and padding.
function isFullBleed(pathname: string) {
  const p = (() => {
    try {
      return decodeURIComponent(pathname);
    } catch {
      return pathname;
    }
  })();
  // Static export enables trailingSlash, so the list page's own pathname is "/function/tasks/",
  // strip the trailing slash before the prefix test, or the list page would be mistaken for a detail page and lose the global header.
  const normalized = p.replace(/\/+$/, "");
  return normalized.startsWith("/function/tasks/");
}

const RELEASES_URL = "https://github.com/BaaRai/ARTEX/releases";
const UPDATE_DISMISSED_KEY = "artex-update-dismissed";

function PageCrumb() {
  const pathname = usePathname();
  const active = resolveActiveRoute(pathname);

  return (
    <div className="flex min-w-0 items-center gap-2">
      {active?.group && (
        <>
          <span className="label-micro hidden text-muted-foreground md:inline">{active.group}</span>
          <ChevronRight className="hidden size-3 shrink-0 text-hairline-strong md:block" aria-hidden />
        </>
      )}
      <span className="truncate font-medium text-[0.8125rem] tracking-tight">{active?.title ?? "Console"}</span>
    </div>
  );
}

export function MainContent({ children }: { children: ReactNode }) {
  const currentUser = useCurrentUser();
  const pathname = usePathname();
  const log = useLiveLog();
  const [version, setVersion] = useState("");
  // Only set when the backend has the opt-in update check on (ARTEX_UPDATE_CHECK) and found a newer release.
  const [latest, setLatest] = useState("");

  useEffect(() => {
    api
      .health()
      .then((h) => {
        setVersion((h.version ?? "").replace(/^v(?=\d)/, ""));
        const l = h.latest_version ?? "";
        setLatest(l && getLocalStorageValue(UPDATE_DISMISSED_KEY) !== l ? l : "");
      })
      .catch(() => setVersion(""));
  }, []);

  const updateBanner = latest ? (
    <div className="flex shrink-0 items-center justify-center gap-2 border-hairline border-b bg-surface-2 px-4 py-1 text-xs">
      <a href={RELEASES_URL} target="_blank" rel="noreferrer" className="underline-offset-2 hover:underline">
        {t("ARTEX {version} is available. See the release notes.", { version: latest })}
      </a>
      <button
        type="button"
        aria-label={t("Dismiss update notice")}
        className="text-muted-foreground hover:text-foreground"
        onClick={() => {
          setLocalStorageValue(UPDATE_DISMISSED_KEY, latest);
          setLatest("");
        }}
      >
        <X className="size-3.5" />
      </button>
    </div>
  ) : null;

  if (isFullBleed(pathname)) {
    return (
      <>
        {updateBanner}
        {children}
      </>
    );
  }

  return (
    <>
      {updateBanner}
      <header
        className={cn(
          "sticky top-0 z-50 flex h-12 shrink-0 items-center border-hairline border-b bg-background/85 backdrop-blur-sm",
          "[html[data-navbar-style=sticky]_&]:sticky [html[data-navbar-style=sticky]_&]:top-0 [html[data-navbar-style=sticky]_&]:z-50 [html[data-navbar-style=sticky]_&]:overflow-hidden [html[data-navbar-style=sticky]_&]:rounded-t-[inherit] [html[data-navbar-style=sticky]_&]:bg-background/60 [html[data-navbar-style=sticky]_&]:backdrop-blur-md",
        )}
      >
        <div className="flex w-full min-w-0 items-center gap-3 px-3 lg:px-5">
          <div className="flex min-w-0 items-center gap-2">
            <SidebarTrigger className="-ml-1 size-7" />
            <span className="h-3.5 w-px shrink-0 bg-hairline-strong" aria-hidden />
            <PageCrumb />
          </div>
          <div className="ml-auto flex shrink-0 items-center gap-1.5">
            <SearchDialog />
            <ConnectionIndicator
              feeds={[
                {
                  id: "logs",
                  label: "Event stream",
                  status: log.status,
                  attempt: log.attempt,
                  lastEventAt: log.lastEventAt,
                },
              ]}
            />
            <MullvadIndicator />
            <DirectEgressToggle />
            {version ? (
              <span className="hidden font-mono text-[0.6875rem] text-muted-foreground tabular-nums xl:inline">
                v{version}
              </span>
            ) : null}
            <LayoutControls />
            <ThemeSwitcher />
            <AccountSwitcher users={[currentUser]} />
          </div>
        </div>
      </header>
      <div className="min-h-0 min-w-0 flex-1 overflow-x-hidden p-4 has-data-[content-padding=false]:p-0 md:p-6 md:has-data-[content-padding=false]:p-0">
        {children}
      </div>
    </>
  );
}
