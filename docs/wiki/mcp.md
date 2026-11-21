# MCP servers

MCP servers extend the agents with tools they do not ship with. They are configured in
**System → MCP** and stored in `mcp_servers`.

## 1. A server row

`mcp_servers` (`db/schema.sql:861-889`):

| Column | Meaning |
| --- | --- |
| `name` | unique; becomes the tool prefix |
| `transport` | `stdio`, `http`, `sse` |
| `command`, `args` | stdio only; run as the executor identity |
| `url` | http/sse only |
| `env` | stdio child environment **or** http/sse request headers |
| `enabled` | |
| `insecure` | skip TLS verification (self-signed servers) |
| `timeout_seconds` | per request, default 120 |
| `last_connect_at`, `last_connect_ok`, `last_connect_err` | health, written by the last connect attempt |

**`env` is overloaded by transport.** For `stdio` it is added to the child process environment;
for `http`/`sse` it becomes request headers on every call, which is how a bearer token is passed:
`{"Authorization": "Bearer …"}` (`server/mcpdiscover.go:37`, `mcphttp/client.go:9`). Header names
are validated and a key containing CR/LF is rejected (`mcphttp/client.go:136-144`). Values are
sealed at rest.

## 2. Tools, naming and the cache

A tool is exposed as `mcp__<server>__<tool>` (`mcphttp/client.go:47-48`). Discovery connects,
lists tools, and writes them to `mcp_tools_cache(server_id, tool_name, description, schema,
discovered_at)` (`db/schema.sql:901-909`), so the UI shows tools without a live connection and a
restart does not lose them.

- `POST /api/mcp/{id}/refresh` re-runs discovery for one server (`server/server_mgmt.go:1094-1123`).
- On startup, enabled servers with an **empty** cache are backfilled sequentially with a 90 s
  timeout each (`server/mcpdiscover.go:104-119`). A server whose cache is already populated is not
  reconnected at startup.

The health badge reads exactly those columns: `last_connect_ok=false` shows the first 60 characters
of `last_connect_err` in red, `true` shows the time, `NULL` means never attempted.

## 3. Skill gating

A skill's `mcps:` frontmatter lists server names, e.g. `skills/rea/SKILL.md:4` → `mcps: rea`.

- Assembly connects a server when it is **directly visible** to the agent via `agent_visibility`
  *or* named by a visible skill (`server/assembly.go:77-93`).
- Directly visible and not gated → tools are global from session start.
- Skill-gated → tools are deferred and kept out of the global tool block
  (`server/assembly.go:117-122`). Loading the skill unlocks them via `unlockSkill`
  (`server/assembly.go:127-158`).
- Unlocks are replayed from conversation history on a session rebuild
  (`seedUnlockFromHistory`, `agent/deferred.go:34-50`), so a resumed chat does not lose the tools a
  skill unlocked.

This is why an MCP tool can be listed in `mcp_tools_cache` and still be invisible to the model: it
is gated until its skill loads.

## 4. Egress: MCP traffic follows the task contract

MCP is target-touching traffic, so it obeys the same egress rules as Bash and WebFetch.

- `MCPTaskProxy()` returns a proxy URL only while Mullvad owns task egress — a healthy tunnel, or the
  fail-closed blocked URL. Otherwise it returns empty and operator-configured MCP integrations keep
  their own routing (`server/manager.go:1172-1180`).
- `MCPTaskCA()` returns the MITM CA in parallel (`server/manager.go:1182-1190`).

Both are threaded into every connect path:

| Transport | What receives it |
| --- | --- |
| stdio | `stdioEnvWithProxy` sets `HTTP_PROXY`/`HTTPS_PROXY`/`ALL_PROXY` (+ lowercase), `NODE_USE_ENV_PROXY=1`, and `NODE_EXTRA_CA_CERTS` + `SSL_CERT_FILE` when a CA exists |
| http / sse | `proxyTransport` sets `tr.Proxy` and appends the CA PEM to the system root pool |

**A missing CA is the classic failure.** The recording proxy re-signs every certificate, so an
`npx` MCP child or Go HTTP client without the CA fails the TLS handshake and the server reports
`mcp: connection closed` — a symptom that looks like a dead server. Verified directly:
`curl -x 127.0.0.1:8788 https://mcp.context7.com/mcp` fails with
`SSL certificate problem: unable to get local issuer certificate` and succeeds with `--cacert`.

The browser MCP gets the same treatment earlier: `syncBrowserMCPProxy` appends
`--proxy-server` and sets `NODE_EXTRA_CA_CERTS` when capture is on and strips both when it is off
(`server/manager.go:790-849`).

## 5. What ships seeded

- `ScopeSentry` — `http` transport with an empty URL and `{"X-API-Key":""}`; a placeholder for
  **Asset Sync**. Fill the URL and key, then enable it (`db/schema.sql:897-899`).
- `browser` — `stdio`, command `playwright-mcp`, args `--headless --browser chromium`, disabled by
  default (`db/browser_mcp.go:11-21`).

`filesystem`, `fetch`, `projectdiscovery` and `context7` are not seeded in the schema; they are rows
an operator adds. `filesystem` and `fetch` are typical `npx -y <package>` stdio servers, and the
`npx` cache lives under the executor home (`<data>/exec-home/.npm`), which the executor identity can
write; an MCP child that cannot write its npm log directory exits before answering `tools/list`.
