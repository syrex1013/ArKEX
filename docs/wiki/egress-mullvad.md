# Egress, Mullvad VPN and noise

Task traffic either leaves through a tunnel or is dropped. That is the contract; everything below
exists to keep it true.

## 1. One resolution point

`EffectiveTaskProxyURL()` (`server/manager.go:964-982`) is the single answer to "where does task
traffic go?", resolved in this order:

1. Kernel interface up → `baseProxyAddr()` (the capture MITM if capture is on, else the global
   proxy, else direct).
2. Userspace session with a URL and `Healthy()` → that session's local proxy.
3. Mullvad enabled but no healthy session → `blockedProxyURL = "http://127.0.0.1:9"`
   (`server/manager.go:954`).
4. Otherwise → `baseProxyAddr()`.

Step 3 is the fail-closed rule: enabled with no tunnel means **no task egress at all**, never a
direct dial. `TaskEgressBlocked()` exposes that state to the UI.

Healthy means `up && handshakeSeen && failStreak < 5`, where `failStreak` counts consecutive tunnel
dial failures and resets on success (`egress/wgproxy/wgproxy.go:48,481-487`).

An enabled-but-down tunnel records nothing, so a `direct` egress stamp only appears when Mullvad is
genuinely off. `egressStamp()` classifies every recorded exchange as `tunneled`,
`proxied:<host>` (global proxy, credentials stripped) or `direct` (`server/manager.go:916-935`).

## 2. Settings

| Key | Meaning | Default |
| --- | --- | --- |
| `mullvad_proxy_enabled` | master switch | `false` |
| `mullvad_proxy_port` | local netstack proxy port | `18101` |
| `mullvad_private_key` | account-registered WireGuard key (sealed) | none |
| `mullvad_relay` | relay hostname; `.mullvad.net` trimmed, resolved from the 1 h relay-list cache | none |
| `mullvad_address` | tunnel interface IP from the device config — required, the relay only forwards packets from it | none |
| `mullvad_mode` | `""`/`auto` · `kernel` · `netstack` | `auto` |

The key is never auto-generated. Fallbacks are `ARKEX_MULLVAD_PRIVKEY` and
`data/mullvad/private.key` (`egress/mullvad/mullvad.go:187-197`). The relay UDP port is always
51820.

## 3. Kernel vs netstack

`auto` (`server/manager.go:1203-1217`) prefers kernel when `wg`, `ip` and `iptables` are on PATH.

**Kernel** (`egress/wgkernel/wgkernel.go`) — a real interface:

- `arkexwg0`, MTU 1420, peer allowed-ips `0.0.0.0/0`, keepalive 25; the key reaches `wg` on stdin
  and never touches disk (`:112-118`).
- An iptables chain `ARKEX_WG` is the kill-switch: default-drop OUTPUT, allowing loopback,
  ESTABLISHED/RELATED, `arkexwg0`, marked control-plane packets, `AllowCIDRs`, and relay UDP
  (`:302-335`). `AllowCIDRs` is `172.16.0.0/12`, `192.168.0.0/16`, `10.0.0.0/8`.
- DNS switches to Mullvad `10.64.0.1`; `host.docker.internal` and the Postgres hostname are pinned
  into `/etc/hosts` **before** the switch (`server/manager.go:1395-1401`) so the database does not
  disappear when DNS moves.
- Control plane — LLM sockets — are `SO_MARK`ed `0x41784c4d` (`egress/controlplane.go:18`) and
  policy-routed to the host gateway, which is how LLM calls stay off the tunnel while everything else
  is forced onto it.

**Netstack** (`egress/wgproxy/wgproxy.go`) — wireguard-go `tun/netstack` with a local HTTP CONNECT
and SOCKS5 listener; no TUN, no `NET_ADMIN`. It only accepts the session after `WaitHandshake(15s)`.

Docker needs `cap_add: NET_ADMIN` (`docker-compose.yml:34-35`) and installs
`wireguard-tools iproute2 iptables` (`Dockerfile:120`). `no-new-privileges` keeps the agent executor
uid from touching the rule set. Without `NET_ADMIN` the server still runs, on netstack, still
fail-closed.

## 4. What is tunneled and what stays direct

| Through task egress | Direct by design |
| --- | --- |
| WebFetch (no direct retry), Bash environment, custom HTTP tools, web search, MCP (stdio/http/sse), enrich probe, authmatrix replay, MITM upstream, Playwright / browser MCP, sandbox containers | LLM providers (marked control plane), notifications (own transport + SSRF guard), PostgreSQL |

`web_search_proxy` is ignored while Mullvad is on — one egress path, not two.

## 5. The CA a worker needs

`proxyEnv` (`agent/worker.go:228-251`) builds the Bash environment:

- Always: `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY` (+ lowercase), and `NODE_USE_ENV_PROXY=1` so Node
  24+ honours proxy vars.
- With a MITM CA: `SSL_CERT_FILE` (curl/urllib/Go/openssl), `CURL_CA_BUNDLE`, `REQUESTS_CA_BUNDLE`
  (python-requests ignores `SSL_CERT_FILE`), `GIT_SSL_CAINFO`, `NODE_EXTRA_CA_CERTS`.
- With capture on, the URL carries a pace marker for Bash only (`egress.MarkProxyURL`), so the proxy
  can pace that connection by the noise profile without double-pacing WebFetch.

Each toolchain reads a different variable. Missing one produces a TLS error that looks like a dead
target, not a configuration gap.

## 6. Watchdog

`watchWGProxy` runs every 15 s (`server/manager.go:1314-1373`):

- Kernel: rebuild when the handshake is ≥ 3 min old (rekey is ~120 s, REJECT_AFTER_TIME 180 s).
- Netstack: rebuild when the session is unhealthy.
- Skipped after a manual Down (`wgAutoDown`) or with no key/address/relay.

A rebuild is `startWGProxy` with a 30 s context: kernel does `Down` + `Up` with rollback on failure;
netstack restarts and gates on a 15 s handshake. On success it repoints egress and rebuilds the
agents. At boot, `syncWGProxyEgress` runs before the tunnel returns, so a restart is fail-closed from
the first request.

## 7. Noise governor

`ARTEX_NOISE_PROFILE` or the `noise_profile` setting (the setting wins) paces ARTEX's own requests
to targets (`egress/egress.go:43-56`):

| Profile | RPS | Burst | Per-host | Global | Jitter | Cache | Retries / backoff |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `off` (default) | passthrough | — | — | — | — | — | — |
| `quiet` | 2 | 4 | 1 | 8 | 0.5 | 1 min | 3, 2 s → 1 min |
| `balanced` | 5 | 10 | 2 | 16 | 0.2 | 30 s | 3, 1 s → 30 s |
| `aggressive` | 20 | 40 | 4 | 32 | 0.1 | 10 s | 2, 500 ms → 10 s |

Jitter is capped at 250 ms. A non-`off` profile also sets `ARTEX_TARGET_USER_AGENT` on requests that
carry no user agent. 429/503 honour `Retry-After`; a value past the profile's max delay is not
retried. With a profile set, agent prompts also carry the low-and-slow flags for nmap, nuclei,
httpx, katana, ffuf, dalfox and sqlmap.
