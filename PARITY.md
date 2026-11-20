# ArKex ↔ ARTEX Parity + V2 Upgrades

Baseline: [BaaRai/ARTEX](https://github.com/BaaRai/ARTEX).

## Verdict

**UI / feature-surface parity: restored.** ArKex keeps ARTEX tabs/APIs and adds ArKex 2.0 upgrade surfaces from `ARKEX_V2_BLUEPRINT.md` / `ARKEX_V2_CODING_PROMPT.md`.

## Nav checklist (ARTEX)

### Functions
| Tab | Path | Status |
| --- | --- | --- |
| Dashboard | `/dashboard` | ✅ |
| Chat | `/chat` | ✅ (shared team chat) |
| Tasks | `/function/tasks` | ✅ |
| Findings | `/function/findings` | ✅ |
| Traffic | `/function/traffic` | ✅ |
| Tool Execution | `/function/commands` | ✅ |
| LLM Recording | `/function/llm-records` | ✅ |
| Assets | `/function/assets` | ✅ |
| Asset Sync | `/function/sync` | ✅ |
| Workspace | `/function/workspace` | ✅ |
| 0-Day Lab | `/lab` | ✅ ArKex (desync/CPG/fuzz/OAST wired) |

### System
| Tab | Path | Status |
| --- | --- | --- |
| LLM | `/system/llm` | ✅ (+ refusal→Tier-3 failover in `llmpool`) |
| Agent / MCP / Skill / Tools / Notify / Intercept / Logs / Settings | `/system/*` | ✅ |
| Storage Governor card | Settings | ✅ `GET /api/system/storage` |
| Team accounts | Settings | ✅ `GET/POST /api/users` |

### Auth
| Page | Path | Status |
| --- | --- | --- |
| Login | `/login` | ✅ team usernames |
| Setup | `/setup` | ✅ |

## ArKex V2 upgrade packages (in module graph)

| Package | Role |
| --- | --- |
| `storage/` | Governor, Zstd codec, file CAS, PG CAS (`blobs`) |
| `sandbox/` | Opt-in Docker intent sandbox (`ARKEX_SANDBOX=1`) |
| `offensive/desync` | CL.TE / TE.CL / H2 / single-packet probes |
| `offensive/oast` | Payload mint + callback correlation (S3) |
| `offensive/authmatrix` | Multi-identity vault + differential GET |
| `offensive/zeroday` | CPG sink indexer, patch-diff heuristics, fuzz jobs |
| `llmpool` | Refusal / content_filter failover to next profile |

## Import prior ARTEX

`scripts/import-artex.sh --db dump.sql.gz [--data data.tar.gz]`

## Product rule

ArKex = **upgraded ARTEX**. Keep ARTEX routes. Add upgrades as additive surfaces.
