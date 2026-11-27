"use client";

import * as React from "react";

import { Power } from "lucide-react";
import { toast } from "sonner";

import { t } from "@/i18n";
import { api } from "@/lib/api";
import { beginEgressAction, isEgressBusy, subscribeEgressBusy } from "@/lib/egress-busy";
import { getEgressLive, isEgressUnreachable, subscribeEgressLive } from "@/lib/egress-live";
import { cn } from "@/lib/utils";

// Header switch next to the egress pill: flips the server-side direct-egress
// override (settings.egress_direct). On, every tunnel and proxy (Mullvad, the
// global WireGuard config, the global proxy) is bypassed so traffic flows
// straight from the server IP; off restores the stored routing. The egress pill
// reflects the new route via its live stream.
export function DirectEgressToggle() {
  const egress = React.useSyncExternalStore(subscribeEgressLive, getEgressLive);
  const unreachable = React.useSyncExternalStore(subscribeEgressLive, isEgressUnreachable);
  const busy = React.useSyncExternalStore(subscribeEgressBusy, isEgressBusy);
  const [saving, setSaving] = React.useState(false);

  const direct = egress?.egress_direct === true;
  const disabled = saving || busy || unreachable || !egress;

  const toggle = () => {
    const next = !direct;
    setSaving(true);
    const endEgress = beginEgressAction();
    api
      .setSettings({ egress_direct: next })
      .then(() => {
        toast.success(
          next
            ? t("Direct egress on · Mullvad, WireGuard and the global proxy are bypassed")
            : t("Direct egress off · stored routing restored"),
        );
      })
      .catch((e) => toast.error(t("Toggle failed: {error}", { error: (e as Error).message })))
      .finally(() => {
        endEgress();
        setSaving(false);
      });
  };

  const description = direct
    ? t(
        "Direct egress is on: Mullvad, WireGuard and the global proxy are bypassed, traffic flows straight from the server IP",
      )
    : t("Direct egress is off: task traffic follows Mullvad / WireGuard / the global proxy as configured");

  return (
    <button
      type="button"
      onClick={toggle}
      disabled={disabled}
      aria-pressed={direct}
      aria-label={t("Direct egress")}
      title={description}
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border bg-background px-2 py-0.5 font-medium text-xs tabular-nums",
        direct
          ? "border-emerald-600/40 text-emerald-700 dark:text-emerald-400"
          : "text-muted-foreground hover:bg-accent",
        disabled && "cursor-not-allowed opacity-60 hover:bg-transparent",
      )}
    >
      <Power
        className={cn(
          "size-3.5 shrink-0",
          direct && "text-emerald-600 dark:text-emerald-400",
          (saving || busy) && "animate-pulse",
        )}
        aria-hidden
      />
      <span className="truncate">{t("Direct")}</span>
    </button>
  );
}
