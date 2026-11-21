# Arkex wiki

Operational and code facts for this fork. Pages here cover behaviour that the README and
existing design docs do not spell out at the level an operator or a debugger needs. Where a
topic is already documented, this wiki links to it instead of restating it.

## Start here

| Need | Page |
| --- | --- |
| Run it, configure it, back it up | [docs/OPS.md](../OPS.md) |
| What the product does, how to install | [README.md](../../README.md) |
| Code-level map: packages, flow, trust boundaries | [docs/ARCHITECTURE.md](../ARCHITECTURE.md) |
| Change history | [CHANGELOG.md](../../CHANGELOG.md) |
| How a task advances and completes | [task-lifecycle.md](task-lifecycle.md) |
| LLM profiles, failover, health, accounting | [llm-and-failover.md](llm-and-failover.md) |
| MCP servers and tools | [mcp.md](mcp.md) |
| Egress, Mullvad, noise profiles | [egress-mullvad.md](egress-mullvad.md) |
| Symptom → cause → fix | [troubleshooting.md](troubleshooting.md) |

## The shape of the system

- **One binary, one database.** The Next.js UI is embedded with `go:embed`
  (`server/webui_embed.go`, build tag `embedui`); PostgreSQL is the only external dependency.
- **Two graphs.** The *exploration graph* is per task and records what was tried and with what
  result; the *asset graph* is shared across tasks and is the source of truth for
  `root_domain / subdomain / ip / service / app / endpoint`. Anchors link them, which is what
  makes per-asset test coverage computable. Details: [ARCHITECTURE.md](../ARCHITECTURE.md) §1.
- **Three LLM roles drive a task:** `planner` judges goals and emits intents, `worker` executes
  one intent at a time and writes facts/findings back, `mainagent` holds the operator
  conversation. `goals` decomposes an initial description and `retester` re-runs one finding.
- **Everything is auditable.** Intents, facts and findings are nodes; tool calls are recorded by
  the MITM proxy on `:8788`; token spend lands in `llm_usage`. Nothing depends on a chat log.

## Conventions in these pages

- `path:line` references point at the code that owns the behaviour.
- Settings names (e.g. `llm_pool_enabled`) are the database keys edited in **System** pages;
  `ARTEX_*` names are environment variables read at startup. Precedence between the two is
  documented in [OPS.md](../OPS.md) §2.
- Every constant, default and error string quoted here was read from the code, not inferred.
