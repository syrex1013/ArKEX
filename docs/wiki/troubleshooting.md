# Troubleshooting: symptom → cause → what to check

Each entry names the exact log line, the mechanism behind it, and the check that distinguishes it
from a real failure. Start with the log line, not the code.

## MCP servers report down or `mcp: connection closed`

**Log:** `[mcp] <name> connect failed: mcp: connection closed`, or `…: EOF`,
`…: The backend process received SIGINT or SIGTERM and is restarting` for HTTP servers.

Two distinct causes:

1. **Missing CA on a re-signed certificate.** The recording proxy re-signs every target certificate.
   A stdio child or Go HTTP client without the egress CA fails the handshake, and the child exits
   before answering `tools/list`. Check with the same proxy and CA:
   ```bash
   curl -x "$PROXY" https://mcp.context7.com/mcp                       # fails
   curl --cacert "$CA" -x "$PROXY" https://mcp.context7.com/mcp       # succeeds
   ```
   The fix is that `MCPTaskCA()` is passed to every transport: `stdioEnvWithProxy` sets
   `NODE_EXTRA_CA_CERTS`/`SSL_CERT_FILE`, and `proxyTransport` adds the PEM to `RootCAs`
   (`mcphttp/client.go`). An `insecure: true` row also works but should not be needed.
2. **The server is genuinely down.** `projectdiscovery` and `context7` are third-party endpoints.

If `last_connect_ok` is already `true` and the log line is old, nothing is wrong: startup
auto-discovery only reconnects servers whose cache is empty. Press **Refresh** on the row to retest.

## `HTTP 400 {"code":"1210"} This model always engages in thinking and cannot be disabled`

**Cause:** a request overrode thinking to `disabled` for a model that always thinks. The profile is
not broken and no retry will help; the old behaviour marked the profile unusable, which then looked
like a dead provider.

**Fix:** do not send `thinking_type: disabled` for such a model. An empty `thinking_type` omits the
field (`agent/provider.go`), which is what the compactor, side-question summariser and intercept
judge now do. If you need to steer one, use a supported `reasoning_effort` value.

## `[compaction] compress exp=N block=[…]: empty body from model`

**Cause:** the visible body was empty. On a reasoning model the output budget is spent on thinking
first, so a small cap truncates the answer to nothing. The compactor now uses `MaxTokens: 4096`
(`agent/compaction.go`).

**Note:** this is a *background* compaction failure. The planner keeps working with the uncompacted
nodes, so treat it as a warning, not an outage. Confirm with:
```sql
SELECT id, created_at, text FROM server_logs
WHERE tag = 'compaction' ORDER BY id DESC LIMIT 5;
```

## `noa request budget exceeded: estimate N exceeds configured budget M`

**Cause:** the request genuinely does not fit the declared window; the guard refuses before any
network call (`agent/noa_budget.go`). It is not a transport error.

**Fix:** raise `context_window_k` for that profile, or start a new session so the protected context is
smaller. Retrying is pointless. For a task chain, the row is marked `quota_exhausted`-equivalent only
for real quota errors, so this one needs a window change, not a different profile.

## A task looks idle but is not done

Check in this order:

1. **Quiet mode.** `plan_quiet_mode` drops an *edge* round when the open-goal fingerprint has not
   changed and an intent is still active. The log line is
   `[planner] task N quiet: work in flight, goals unchanged, edge round skipped`. Expected.
2. **An intent is still running.** Look at `activity` for the worker. A claimed intent holds a 90 s
   lease with a 15 s heartbeat; a lost lease cancels the work with `AbortWorkClaimLost`.
3. **A goal lacks acceptable evidence.** `prove_goal` refuses unless the evidence is a finding, or a
   fact with confidence `observed` whose source is not `target_response`. The goal then legitimately
   stays open.

`goal_met` is unbound by default: a planner will not declare the task done without per-goal proof.

## Pause shows `run interrupted: The user paused the task; stopped during the model output stage`

Expected. Pause cancels the context **and** aborts the in-flight model call, so the planner records an
interruption instead of a completed turn. It is not a crash. The same wording appears as
`context canceled` in target-traffic logs for requests that were in flight.

## Provider refusals are not bugs

A turn the provider's safety system declines has no text and no tool calls. It is recorded as
`ErrProviderRefusal` and *is* eligible for failover, which is different from the old behaviour of
treating it as a normal completion. If every profile refuses, the task will keep refusing: check the
goal, not the pool.

## Distinguishing failure classes

| Signal | Meaning | Action |
| --- | --- | --- |
| HTTP 400 | malformed request; identical on every profile | fix the request or profile field; failover is disabled for 400 by design |
| `ErrProviderRefusal` | provider safety decline | change the request; failover may move to another provider |
| 401/402/403/404 | hard failure; breaker opens immediately | fix the key/credit/URL |
| 408/429/≥500 | transient | allow failover and cooldown |
| `ErrNoaBudget` / context overflow | request does not fit | raise the window |
| `EOF` / `connection closed` / `context canceled` | transport or cancellation | check egress, CA, and whether the task was paused |

## Collecting evidence

- `GET /api/health` — liveness, build version, provider health.
- `GET /api/logs`, `GET /api/logs/stream` — the backend sink; **System → Logs**.
- `server_logs` (level, tag, text, created_at) persists the same lines, so a restart does not erase
  what happened before it. Filter by `tag` (`mcp`, `compaction`, `planner`, `engine`, `mullvad`).
- `activity` holds the exploration trace (planner/worker text, thinking, tool calls, usage).
- `scripts/verify.sh` reproduces the CI checks locally; `--full` adds `-race` and the web build.
- `llm_usage` for spend, `llm_records` for bodies (**System → LLM Recording**, off by default),
  `llm_raw_debug` for wire bytes (`ARTEX_LLM_RAW_DEBUG=1`, 24 h TTL).

## Is it the tunnel?

```bash
wg show arkexwg0 | grep -E 'peer|handshake'   # kernel mode
```
A handshake older than 3 minutes on a live tunnel means the watchdog should be rebuilding it. If
task traffic is being dropped, `TaskEgressBlocked()` is true and every task transport points at
`http://127.0.0.1:9` — by design. That is a VPN problem, never a target problem.
