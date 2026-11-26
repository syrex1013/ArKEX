package server

// The chain agent's loop: woken by finding/path signals (t.chainNotify) or by a
// 15-minute idle timer, it correlates the task's findings into attack paths and
// validates or refutes their steps. State lives in attack_paths + chain_cursor,
// so every round is incremental.

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
)

// chainIdleInterval is the fallback wake-up when no finding/path signal arrives.
const chainIdleInterval = 15 * time.Minute

// SetChainResolver installs the per-task chain-agent resolver (wired by the
// server from the task agent bundle). nil result = the task has no runnable
// chain agent (no binding / exhausted chain) and the round is skipped.
func (e *Engine) SetChainResolver(fn func(t *Task) *agent.Chain) { e.resolveChain = fn }

// chainLoop waits on the chain wake signal or the idle timer, then runs one
// correlation round. It exits only when the task context is cancelled or the
// task is being deleted.
func (e *Engine) chainLoop(ctx context.Context, t *Task) {
	timer := time.NewTimer(chainIdleInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.chainNotify:
		case <-timer.C:
		}
		// A signal may coalesce several wakes: chainNotify is unbuffered and
		// NotifyChain is non-blocking, so a burst folds into the single drain below.
		select {
		case <-t.chainNotify:
		default:
		}
		if !e.chainRound(ctx, t) {
			return
		}
		resetPlannerTimer(timer, chainIdleInterval)
	}
}

// chainRound guards, admits and runs one chain-agent session. Returns false when
// the loop should stop (task context cancelled or the task is being deleted);
// a skipped round (paused/terminal/disabled/no runtime) is true — keep waiting.
func (e *Engine) chainRound(ctx context.Context, t *Task) bool {
	select {
	case <-ctx.Done():
		return false
	default:
	}
	if e.IsDeleting(t.ID) {
		return false
	}
	if e.IsPaused(t.ID) || e.isSettling(t.ID) || isTerminalStatus(e.m.TaskStatus(t.ID)) {
		return true
	}
	if !e.m.ChainAgentEnabled() {
		return true
	}
	chain := e.resolveChain(t)
	if chain == nil {
		return true
	}
	// One task admission for the whole session, so a delete cannot observe
	// quiescence between the LLM return and the final path/cursor writes.
	if !e.beginTaskOperation(t.ID) {
		return false
	}
	defer e.decInflight(t.ID)

	ectx := e.clockCtx(e.execContextFor(ctx, t.ID), t, false)
	emit := func(a db.Activity) {
		a.Worker = "chain"
		e.emitActivity(t, a)
	}
	runCtx := intercept.WithTaskContext(ectx, t.ID, "chain", emit)
	runCtx = intercept.WithTargetScope(runCtx, t.targetScope(intercept.DefaultTargetWaitBound))

	taskIDInt, _ := strconv.ParseInt(t.ID, 10, 64)
	e.BeginLLMCall(t.ID)
	err := chain.Run(runCtx, taskIDInt, e.m.assets, t.Store, t.TargetGuard.Hooks(), emit)
	e.EndLLMCall(t.ID)
	if err != nil {
		log.Printf("[agent-lifecycle] task=%s agent=chain kind=run FAILED: %v", t.ID, err)
	}
	time.Sleep(2 * time.Second) // let the graph writes settle before the next wake
	return true
}
