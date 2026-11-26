// Shared flag for in-flight egress actions: tunnel bring-up / teardown, relay
// pings, and global WireGuard config saves. The top-bar egress pill shows its
// "Pinging…" state while the counter is above zero, so every page that starts
// such an action brackets it with beginEgressAction().
let busyCount = 0;
const listeners = new Set<() => void>();

function emit() {
  for (const listener of listeners) listener();
}

export function beginEgressAction(): () => void {
  busyCount += 1;
  emit();
  let done = false;
  return () => {
    if (done) return;
    done = true;
    busyCount = Math.max(0, busyCount - 1);
    emit();
  };
}

export function subscribeEgressBusy(onChange: () => void): () => void {
  listeners.add(onChange);
  return () => {
    listeners.delete(onChange);
  };
}

export function isEgressBusy(): boolean {
  return busyCount > 0;
}
