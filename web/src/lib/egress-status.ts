import type { MullvadEgress } from "./types";

type Route = "mullvad" | "proxy" | "direct";
type State = "checking" | "working" | "failed" | "off" | "unverified" | "unavailable";

export function egressStatus(
  egress: MullvadEgress | undefined,
  unreachable = false,
): { route: Route; state: State; ip?: string } {
  const route = egress?.up || egress?.task_egress_blocked ? "mullvad" : egress?.global_proxy_set ? "proxy" : "direct";
  if (unreachable) return { route, state: "unavailable" };
  if (!egress) return { route, state: "checking" };
  if (route === "proxy") return { route, state: "unverified" };
  if (route === "direct") return { route, state: "off", ip: egress.egress_ip || undefined };
  if (egress.task_egress_blocked) return { route, state: "failed" };
  if (egress.egress_tunneled && egress.egress_ip) return { route, state: "working", ip: egress.egress_ip };
  return { route, state: "unverified" };
}
