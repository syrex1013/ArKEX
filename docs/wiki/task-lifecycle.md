# Task lifecycle: goals, planner, workers

A task is a goal tree plus an execution loop. The loop never runs "the plan"; it runs
*one intent at a time* and re-plans whenever the graph changes. This page documents what
actually gates those transitions.

## 1. Task states

`tasks.status` is `created · running · paused · done · failed · timeout`
(`db/schema.sql:532-534`). `done`, `failed` and `timeout` are terminal (`db/tasks.go:88-90`
`IsTerminal`).

| Transition | Where enforced |
| --- | --- |
| `created` → `running` | admission, `server/goals.go:188-190`; the loop itself starts in `server/engine.go:836-868` |
| `running` → `paused` | `server/task_control.go:102-135`; refuses a terminal or already-paused task, cancels the main-agent chat |
| `paused` → `running` | `server/task_control.go:138-141` |
| → `done` | last open goal met, `server/engine.go:1022-1032` |
| → `timeout` / budget stop | `SetTerminalStatusGuarded`, `server/engine_timeout.go:266-270` |

Status writes are guarded compare-and-set (`db/tasks.go:508-523`), so a `done` racing a
`timeout` cannot overwrite the other. `completed_at` is stamped once and cleared if the task
leaves a terminal state.

**Pause is not cancel.** Pausing cancels the context *and* aborts the in-flight model call
(`agent/cancelcause.go`), so the planner records `run interrupted: …` rather than finishing its
turn. Cancelling one intent is separate: `server/engine.go:494-496` accepts only `pause` and
`cancel`; a cancelled intent becomes `state='deleted'` with a `delete_reason`.

## 2. The exploration graph

`exploration_nodes` kinds: `begin, goal, intent, fact, finding, hint, digest`
(`db/schema.sql:171-195`). States are per-kind:

| Kind | States |
| --- | --- |
| intent | `open, running, paused, done, blocked, exhausted, stopped, deleted` |
| goal | `open, met, abandoned` |
| fact | `confirmed, dismissed, origin` |
| finding | `confirmed, dismissed` |
| hint | `active, consumed` |
| digest | `active, superseded` |

Edges (`rel`): `spawns, derived_from, yields, proves, covers`.

Which tool writes what:

| Tool | Writes |
| --- | --- |
| `add_intent` | intent node + `spawns` edge from its goal (planner only) |
| `record_fact` | fact node; `source` is one of `target_response, tool_output, third_party_intel, operator_input, inference` |
| `report_finding` | finding node |
| `add_hint` / `add_task_hint` | hint nodes |
| `insert_assets` | asset graph rows, anchored to the exploration node |
| `record_coverage` | coverage ledger row for a surface/risk class |
| `prove_goal` | `proves` edge + goal state `met` |
| compactor | `digest` node, `origin='compactor'` |

## 3. What wakes the planner

Two triggers, both debounced:

- **Edge.** Any graph change calls `t.Notify`, coalesced by an 800 ms debounce
  (`server/engine.go:457,930-941`).
- **Heartbeat.** `PlanHeartbeatSeconds` re-arms a timer (default and floor 600 s,
  `db/tasks.go:145-160`, `server/engine.go:874-880`), re-armed after every wake.

**Quiet mode** (`plan_quiet_mode`, default on) suppresses an *edge* round when the open-goal
fingerprint is unchanged and an intent is still active (`server/engine.go:885-897`). It drops
the round, it does not stop work. The heartbeat still fires, and it fails open on a store error.
An active task therefore looks idle in the logs while workers are running — this is expected.

## 4. Intent claiming

Workers do not receive a queue item; they claim one:

1. `claimNext` reads `Frontier(20)` then `ClaimIntentToken` (`server/engine.go:1503-1509`).
2. The claim is a lease: `unclaim_token` plus `last_heartbeat_at` (`db/schema.sql:204-210`).
   `IntentClaimTTL` is 90 s, heartbeat every 15 s (`db/exploration.go:33-37`).
3. `claimSweepLoop` reclaims expired claims every 15 s (`server/engine.go:1067-1068`). A worker
   whose heartbeat is rejected with `ErrIntentClaimLost` cancels its work with
   `AbortWorkClaimLost` instead of writing results for an intent it no longer owns.

Concurrency is the `workers` setting, default 3 (`server/manager.go:372,409-421`), read once per
run (`server/engine.go:836`).

A task's worker budget is a *per-worker estimate*: 50,000 tokens and 10 turns until a worker
finishes in this process, then the average of finished workers. That estimate is not restored
across a restart; only spend already in `llm_usage` is.

## 5. Completing a goal, and the task

`prove_goal` (`agent/tools.go:1316-1380`) takes `goal_id, evidence_id, reason`. The evidence must
be a **finding**, or a **fact with confidence `observed`** whose source is not `target_response`
(`agent/tools.go:1342-1359`); anything else is refused and the goal stays open. On success the
goal becomes `met`; when no open goal remains the planner persists `done` and cancels the workers.

`goal_met` declares the whole task finished and refuses while any goal is open, but it ships
**unbound** (`agent/toolcatalog.go:80`), because it bypasses per-goal proof. A migration strips it
from the planner once (`server/orchestration.go:683-697`).

So "the task will not complete" usually means one goal has no acceptable evidence, not that the
planner is broken. Check the goal tree before the logs.

## 6. Cold-digest compaction

Large graphs are condensed in the background; the planner then reads digests instead of every
cold node.

- `maintain` runs synchronously each planner round: bumps `explorations.round_no`, stamps or clears
  `cold_since_round` (`agent/compaction.go:34-68`).
- `minor` (threshold 20) and `major` (threshold 8, 60 s cooldown) run off the hot path.
- A node is cold after ≥ 6 inactive rounds; a block needs ≥ 2 nodes; only intent and fact nodes
  fold (`agent/coldgraph.go:41-45,97`).
- A digest keeps `covers` edges to its members and the members themselves are never deleted
  (`db/digest.go:132,159,215`), so nothing is lost — it is a cache, not a summary of record.

The compactor runs on the same profile the task runs on. It inherits thinking mode deliberately:
forcing `disabled` makes always-thinking models answer HTTP 400 (`code 1210`), which used to burn
the profile as unusable. Its `MaxTokens` is 4096 so a reasoning model still emits a visible body.

`noa_compaction` (off by default, `server/manager.go:354,629-633`) replaces this with Norma's
model-driven compaction for planner/worker/main agent/chat; it is read once per run, so toggling it
affects only runs started afterwards. If the integration fails it falls back to the built-in
compactor.

## 7. Budgets

`agent/budget.go:84-93`:

| Env | Scope |
| --- | --- |
| `ARTEX_TASK_BUDGET_TOKENS` / `_TURNS` | whole task incl. planner and workers |
| `ARTEX_INTENT_BUDGET_TOKENS` / `_TURNS` | one worker run |

Unset, `0` or invalid means unlimited. A child's effective budget is the minimum of its own and the
parent's remaining budget above the reserve. The wrap-up reserve is 5 % with floors of 20,000
tokens and 2 turns (`agent/budget.go:23-25`). Inside the reserve a planner round is forced to wrap
up, and a worker may only call `record_fact`, `insert_assets`, `report_finding`,
`update_finding_report` and `record_coverage`. A worker is not started at all when the remaining
budget cannot cover the per-worker estimate.

## 8. The retester agent

`retester` re-runs one existing finding to decide whether it still reproduces. It runs as a
conversation and only the first turn executes the retest (`server/conversations.go:548-576`).
Its two tools, `get_finding_retest_context` and `record_finding_retest_result`, derive the finding
from server-owned conversation context, so the model cannot redirect them at another finding
(`server/finding_retests.go:100-108,142-169`). Start one with
`POST /api/exploration/findings/{id}/retests`; rows live in `finding_retests` with verdict
`reproduced · fixed · inconclusive`.
