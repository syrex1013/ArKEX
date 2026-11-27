// Shared live egress snapshot for the top-bar indicator and the direct-egress
// toggle: one EventSource on /api/mullvad/egress/stream feeds every consumer,
// so mounting the pill on several pages doesn't multiply public-IP probes.
import { api, sseUrl } from "@/lib/api";
import type { MullvadEgress } from "@/lib/types";

let egress: MullvadEgress | undefined;
let unreachable = false;
let started = false;
const listeners = new Set<() => void>();

function emit() {
  for (const listener of listeners) listener();
}

function ensureStarted() {
  if (started) return;
  started = true;
  // First paint before the SSE handshake finishes.
  void api
    .mullvadEgress()
    .then((result) => {
      egress = result;
      unreachable = false;
      emit();
    })
    .catch(() => {
      unreachable = true;
      emit();
    });

  const es = new EventSource(sseUrl("/api/mullvad/egress/stream"));
  es.onmessage = (e) => {
    try {
      egress = JSON.parse(e.data) as MullvadEgress;
      unreachable = false;
      emit();
    } catch {
      /* ignore malformed frame */
    }
  };
  // EventSource auto-reconnects; keep the last known state across gaps.
}

export function subscribeEgressLive(onChange: () => void): () => void {
  ensureStarted();
  listeners.add(onChange);
  return () => {
    listeners.delete(onChange);
  };
}

export function getEgressLive(): MullvadEgress | undefined {
  ensureStarted();
  return egress;
}

export function isEgressUnreachable(): boolean {
  ensureStarted();
  return unreachable;
}
