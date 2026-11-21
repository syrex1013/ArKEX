# LLM providers, failover and accounting

Every LLM call goes through a *profile*: a stored endpoint + model + key. Three things decide
which profile serves a call — agent binding, the task's chain, then the global pool.

## 1. Profile fields

Table `llm_profiles` (`db/schema.sql:370-418`), edited in **System → LLM**.

| Column | Meaning |
| --- | --- |
| `name` | unique label shown everywhere |
| `format` | `openai`, `anthropic`, `openai-responses`, `ollama` (`db/schema.sql:433`) |
| `base_url` | endpoint; `ollama` normalises to `:11434/v1` |
| `model` | model id |
| `api_key` / `api_key_hint` | sealed secret (AES-256-GCM); hint is what the UI shows |
| `proxy` | per-profile LLM proxy (`http`/`https`/`socks5`) |
| `rate_per_second`, `rate_per_minute` | client-side throttle |
| `context_window_k` | declared window; `0` = unset, default treated as 200K |
| `max_tokens` | `0` = do not send the field |
| `max_tokens_field` | `''` or `max_completion_tokens` for endpoints that require it |
| `thinking_type` | `''` (omit) · `disabled` · `enabled` |
| `reasoning_effort` | `''` · `low` · `medium` · `high` · `xhigh` · `max` |
| `streaming` | stream by default |
| `priority` | larger is earlier in the pool order |
| `pool_exclude` | `true` = not a failover target, still bindable |
| `is_default` | at most one row (enforced) |
| `session_header_key`, `user_agent`, `retry_*` | transport extras |
| `price_*_per_m` | used for cost display only |

Two fields are deliberately tri-state. An empty `thinking_type` or `reasoning_effort` **omits** the
field from the request; that is not the same as sending `disabled`. Sending an explicit `disabled`
to a model that always thinks returns HTTP 400 with `code 1210`
(`This model always engages in thinking and cannot be disabled`). This is why the compactor,
side-question summariser and intercept judge no longer override thinking.

`context_window_k` has two consumers with different defaults (`agent/provider.go:178-212`):
`CompactionWindow()` clamps to [32K, 1M] with a 200K default (compaction arithmetic needs a floor),
while `ContextWindowTokens()` has no floor, so an 8K profile still fails over instead of slipping
past the pool pre-filter.

## 2. Environment configuration

`FromEnv` (`agent/provider.go:228-287`) is only used when no profile is configured:

| Env | Effect |
| --- | --- |
| `ARTEX_LLM_PROVIDER` | `openai` · `anthropic` · `openai-responses` · `ollama`; unset infers from keys |
| `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` | infer the provider when unset |
| `ARTEX_LLM_BASE_URL`, `ARTEX_LLM_MODEL`, `ARTEX_LLM_PROXY` | endpoint overrides |
| `ARTEX_LLM_STREAM` | `false`/`0`/`off` disables streaming; streaming is the default |

`ollama` needs no key and deliberately does not inherit `OPENAI_API_KEY`
(`agent/provider.go:270-273`). A cloud format with an empty key reports "not configured".

## 3. Selection order

`currentFor` (`server/task_llm.go:210-267`):

1. **Agent binding** — the profile bound to this agent, if any.
2. **Task chain** — rows in `task_llm_profiles(task_id, profile_id, position, status)`
   (`db/schema.sql:721-734`) ordered by `position`, with `tasks.active_llm_profile_id` tracking the
   current member. If every member is `quota_exhausted` the call fails with `chainExhausted`.
3. **Global provider** — via the pool.

## 4. Failover

`shouldFailover` (`llmpool/pool.go:289-308`) decides per error:

| Outcome | Fail over |
| --- | --- |
| context cancelled / deadline | **no** |
| any HTTP **400** | **no** — a malformed request fails identically everywhere |
| `ErrProviderRefusal` | yes |
| no HTTP status (transport) | yes |
| 401, 402, 403, 404, 408, 429 | yes |
| ≥ 500 | yes |

A **hard** failure is exactly 401/402/403/404 (`llmpool/pool.go:315-321`): it opens the breaker on
the first occurrence. Everything else needs 3 consecutive failures.

Two settings gate it, both off by default:

- `llm_pool_enabled` — failover for the global active-profile path.
- `llm_pool_bind_fallback` — whether an agent or task *bound* to a profile may fall back to the
  chain. Off means the binding is exclusive and a failure is a failure.

Inside a task, two more escalations advance the chain without waiting for the streak
(`server/task_llm.go:98,436-441,599-604`):

- **Quota exhaustion** — only when the error explicitly names quota/credits/billing
  (`isQuotaExhaustedError`, deliberately strict). The profile is marked at the current
  `llm_chain_revision`, so a stale error cannot exhaust a freshly edited chain.
- **Context overflow** (`isContextOverflowError`, `server/task_llm.go:712-725`) — `agent.ErrNoaBudget`,
  or a message containing `too long`, `context length`, `maximum context`, or `status 413`. The chain
  advances immediately instead of surfacing after three attempts, and the same-provider stream retry
  is skipped.

A third consecutive non-quota failure also advances (`llmFailStreakThreshold = 3`), pre-commit only.

## 5. Health and cooldown

The authoritative breaker is in memory (`llmpool/health.go`), mirrored asynchronously to
`llm_profile_health(profile_id, fails, trips, open_until)` (`db/schema.sql:494-499`) and restored at
startup with expired windows dropped.

Cooldown ladder by trip count: 1 min, 5 min, 30 min (`llmpool/health.go:22`). A success (`Pass`)
clears the row. **Recover now** in the UI calls `Registry.Reset`. Health is shown per profile and
per task path, so an explicit chain no longer shows "Healthy" through a real outage.

## 6. Budget guards

`NewBudgetGuard` (`agent/noa.go:77-79`) wraps a provider and refuses a request whose irreducible
context exceeds the declared window **before any transport call**, with
`ErrNoaBudget = "noa request budget exceeded"` (`agent/noa_budget.go:19`). That is the origin of the
`noa request budget exceeded: estimate N exceeds configured budget M` error — the request genuinely
does not fit the window, and the message says to raise the window or start a new session. It is not
a transport bug and retrying will not help.

## 7. Token accounting: three tables, three purposes

| Table | Contents | Switch |
| --- | --- | --- |
| `llm_usage` | one light row per call: task, worker role, model, profile, latency, input/output tokens, cache read/write, status | always written |
| `llm_records` | full per-call record incl. error and request/response bodies, capped at 1 MiB per attempt and 8 attempts per call | setting `llm_record`, off by default |
| `llm_raw_debug` | raw wire bytes (`BYTEA`) for debugging a provider | `ARTEX_LLM_RAW_DEBUG=1` (or `ARKEX_LLM_RAW_DEBUG`), 24 h TTL purge |

Usage metering runs even when `llm_record` is off — only bodies are skipped. An empty LLM recording
page with the toggle off is expected, and the UI says so.
