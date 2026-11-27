import type { MullvadEgress } from "./types";

type Route = "mullvad" | "wg" | "proxy" | "direct";
type State = "checking" | "pinging" | "working" | "failed" | "off" | "unverified" | "unavailable";

export function egressStatus(
  egress: MullvadEgress | undefined,
  unreachable = false,
  busy = false,
): { route: Route; state: State; ip?: string } {
  // Mullvad wins while its tunnel is up (EffectiveTaskProxyURL order); a stored
  // global wg config owns the indicator otherwise, then Mullvad fail-closed, then
  // proxy/direct. The direct-egress override outranks everything: it bypasses
  // every tunnel and proxy by design, so the pill always shows direct.
  let route: Route = "direct";
  if (egress?.egress_direct) route = "direct";
  else if (egress?.up) route = "mullvad";
  else if (egress?.global_wg_set) route = "wg";
  else if (egress?.task_egress_blocked) route = "mullvad";
  else if (egress?.global_proxy_set) route = "proxy";
  if (busy) return { route, state: "pinging" };
  if (unreachable) return { route, state: "unavailable" };
  if (!egress) return { route, state: "checking" };
  if (route === "proxy") return { route, state: "unverified" };
  if (route === "direct") {
    // With the override on, direct is an active route: green once the public-IP
    // probe confirms traffic flows straight from the server. Plain direct (no
    // tunnels/proxies configured) keeps the passive "off" state.
    if (egress.egress_direct && egress.egress_ip) return { route, state: "working", ip: egress.egress_ip };
    return { route, state: "off", ip: egress.egress_ip || undefined };
  }
  if (route === "wg") {
    if (egress.global_wg_up && egress.egress_ip) return { route, state: "working", ip: egress.egress_ip };
    if (!egress.global_wg_up) return { route, state: "failed" };
    return { route, state: "unverified" };
  }
  if (egress.task_egress_blocked) return { route, state: "failed" };
  if (egress.egress_tunneled && egress.egress_ip) return { route, state: "working", ip: egress.egress_ip };
  return { route, state: "unverified" };
}
