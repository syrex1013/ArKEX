package agent

// Chain is the durable chain analyst: it consumes the task's findings since its
// own cursor (incremental — no transcript resume needed), correlates findings and
// assets into scored attack paths, and validates or refutes each step. One run =
// one fresh session; state lives in attack_paths and chain_cursor.

import (
	"context"
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/transcript"
)

// Chain runs one incremental correlation session per wake-up.
type Chain struct {
	prov              llm.Provider
	model             string
	tx                *transcript.Store // raw LLM conversation persistence (nil = off)
	window            int               // context window in tokens (for compaction)
	windowFn          func() int        // optional dynamic task-chain minimum
	maxTurns          int               // max agent turns per run (0 = unlimited)
	proxyAddr         string            // recording proxy for WebFetch (empty = direct)
	proxyCACert       string            // recording proxy's CA cert path (HTTPS verify)
	webSearch         WebSearchOpts     // web_search tool backend selection (off by default)
	workDir           string            // shared work dir (surfaced in prompt as artifact-output target)
	injectConstraints func() bool       // resolver: inject task operation constraints into system prompt? (nil = yes)
	nonStreamingFn    func() bool       // resolver: use non-streaming (Complete) path? (nil = streaming)
	noaEnabledFn      func() bool       // resolver: use experimental noa compaction? (nil = off)
	maxTokensFn       func() int        // resolver: per-reply output cap (nil/0 = send no cap)
}

func NewChain(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int) *Chain {
	return &Chain{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns}
}

func (c *Chain) SetCompactionWindowResolver(fn func() int) { c.windowFn = fn }

func (c *Chain) SetNonStreaming(fn func() bool) { c.nonStreamingFn = fn }

func (c *Chain) nonStreaming() bool { return c.nonStreamingFn != nil && c.nonStreamingFn() }

func (c *Chain) SetNoaEnabled(fn func() bool) { c.noaEnabledFn = fn }

func (c *Chain) SetMaxTokens(fn func() int) { c.maxTokensFn = fn }

func (c *Chain) maxTokens() int {
	if c.maxTokensFn == nil {
		return 0
	}
	return c.maxTokensFn()
}

func (c *Chain) compactionWindow() int {
	if c.windowFn != nil {
		return c.windowFn()
	}
	return c.window
}

func (c *Chain) SetProxy(addr, caCert string) { c.proxyAddr, c.proxyCACert = addr, caCert }

func (c *Chain) SetWebSearch(o WebSearchOpts) { c.webSearch = o }

func (c *Chain) SetConstraintInject(fn func() bool) { c.injectConstraints = fn }

func (c *Chain) wantConstraints() bool { return c.injectConstraints == nil || c.injectConstraints() }

// chainSystem renders the chain agent's system prompt: the DB-overridable body
// plus the same code-owned tails the other built-ins carry.
func chainSystem(workDir string) string {
	body := renderSystem("chain", chainDefaultTmpl, PlannerVars{DataDir: workDir, Now: nowStr()})
	return body + untrustedDataRules + refusalFramingTail() + artifactSpec(workDir) + noiseProfileBlock()
}

// chainFindingsSince renders the findings the chain agent has not consumed yet
// (cap 40, newest first). Empty when the cursor is already at the head.
func chainFindingsSince(ds *db.DB, taskID, cursor int64) (string, int64) {
	rows, err := ds.Query(`SELECT id, vulnclass, name, severity, summary, confidence
FROM findings WHERE task_id=$1 AND id > $2 ORDER BY id DESC LIMIT 40`, taskID, cursor)
	if err != nil {
		return "", 0
	}
	defer rows.Close()
	var b strings.Builder
	var maxID int64
	first := true
	for rows.Next() {
		var id int64
		var vulnclass, name, severity, summary string
		var confidence int
		if err := rows.Scan(&id, &vulnclass, &name, &severity, &summary, &confidence); err != nil {
			return "", maxID
		}
		if maxID == 0 || id > maxID {
			maxID = id
		}
		if first {
			b.WriteString("\n\n[New findings since your last round (finding_id is the ref_id for propose_path steps; confidence is evidence-derived 0-100)]:\n")
			first = false
		}
		fmt.Fprintf(&b, "- finding %d | %s | severity=%s | name=%s | confidence=%d\n  %s\n", id, vulnclass, severity, name, confidence, strings.TrimSpace(summary))
	}
	if first {
		return "", maxID
	}
	return b.String(), maxID
}

// chainOpenPaths renders the non-refuted paths with their step states so the
// agent picks up validation work instead of proposing duplicates.
func chainOpenPaths(ds *db.DB, taskID int64) string {
	paths, err := ds.ListAttackPaths(taskID, []string{db.PathStateProposed, db.PathStateValidating}, 10)
	if err != nil || len(paths) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n[Your open attack paths (validate or refute the proposed steps; do not re-propose the same chain)]:\n")
	for _, p := range paths {
		fmt.Fprintf(&b, "- path #%d | %s | state=%s | score=%.1f\n", p.ID, p.Title, p.State, p.Score)
		if _, steps, err := ds.GetAttackPath(p.ID); err == nil {
			for _, s := range steps {
				ref := fmt.Sprintf("%s #%d", s.Kind, s.RefID)
				if !s.RefExists {
					ref += " (missing)"
				}
				fmt.Fprintf(&b, "    %d. %s | edge=%s | %s -> %s | %s", s.Position, ref, s.Edge, s.Precondition, s.Postcondition, s.Status)
				if s.EvidenceNote != "" {
					fmt.Fprintf(&b, " | note: %s", s.EvidenceNote)
				}
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

// Run executes one chain-agent session. hooks (the task's target-only Guard)
// sees every tool call; may be nil. emit, if non-nil, receives the run's
// activities for the task transcript.
func (c *Chain) Run(ctx context.Context, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, hooks harness.HookRunner, emit func(db.Activity)) error {
	tsx := NewToolSet(ts, "chain")
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetTaskID(taskID)
	if origin, _ := ts.OriginFactID(); origin > 0 {
		tsx.SetOwnerNode(origin)
	}
	base := append(tsx.ChainTools(), defaultTools(c.workDir)...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts)})
	tools, def, cleanup := AugmentTools(WithToolQuery(ctx, "attack path correlation findings assets"), "chain", base)
	defer cleanup()
	def.Deferred = nil

	ds := ts.DB()
	cursor, _ := ds.ChainCursor(taskID)
	findingsBlock, maxFindingID := chainFindingsSince(ds, taskID, cursor)
	pathsBlock := chainOpenPaths(ds, taskID)

	env := newDataEnvelope()
	situational := findingsBlock + pathsBlock
	data := tsx.graphOverviewData()
	situational += renderGraphOverview(data, env)

	// The chain agent has no wall-clock budget of its own; when a deadline exists,
	// clamp MaxDuration to the remainder like a planner round does.
	tc := taskClockFrom(ctx)
	maxDur, clamped := clampMaxDuration(tc.DeadlineUnix, 0)
	settle := wrapupSettlement("chain", nil)
	if tc.DeadlineUnix > 0 {
		settle = wrapupSettlementForTask("chain", nil, clamped)
	}

	taskDir := ensureRunDir(c.workDir, taskID, 0)
	ctx = intercept.WithReviewContext(ctx, taskDir, intercept.ReviewBackground{})
	sysBody := chainSystem(c.workDir)
	if c.wantConstraints() {
		sysBody += constraintBlock(ts)
	}
	system, boundary := deferredSystem(sysBody, def)

	opts := agentcore.Options{
		Provider:        c.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           withWebFetch(ctx, tools, c.proxyAddr, c.proxyCACert),
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		EnableWebSearch:       c.webSearch.Enabled,
		WebSearchBackend:      c.webSearch.Backend,
		BraveSearchAPIKey:     c.webSearch.BraveKey,
		TavilySearchAPIKey:    c.webSearch.TavilyKey,
		KeenableSearchAPIKey:  c.webSearch.KeenableKey,
		KeenableSearchTitle:   KeenableSearchTitle,
		DeepSeekSearchBaseURL: c.webSearch.DeepSeekBaseURL,
		DeepSeekSearchAPIKey:  c.webSearch.DeepSeekAPIKey,
		DeepSeekSearchModel:   c.webSearch.DeepSeekModel,
		WebSearchProxy:        c.webSearch.Proxy,
		BashEnv:               proxyEnv(c.proxyAddr, c.proxyCACert),
		WorkingDir:            taskDir,
		ToolOutputDir:         TaskToolOutputDir(c.workDir, taskID, taskDir),
		MaxTurns:              c.maxTurns,
		MaxDuration:           maxDur,
		Compaction:            compactionConfig(c.compactionWindow()),
		Settlement:            settle,
		NonStreaming:          c.nonStreaming(),
		MaxTokens:             c.maxTokens(),
		UntrustedOutputBoundary: untrustedBoundaryEnabled(),
	}
	if hooks != nil {
		opts.Hooks = hooks
	}
	sessionID := fmt.Sprintf("exp%d-chain", ts.ID())
	ctx = transcript.WithSessionID(ctx, sessionID)
	opts.Hooks = SpillHooks(opts.Hooks, opts.ToolOutputDir)
	opts.Hooks = WithRefusalRerun(opts.Hooks)
	// One fresh session per run; the cursor makes the work incremental, so there is
	// nothing to resume. Raw transcripts still persist when the caller passes a store.
	opts.SessionID = sessionID
	if c.tx != nil {
		opts.Transcript = c.tx
	}
	noaSession := sessionID
	enableNoa(&opts, c.noaEnabledFn, c.workDir, noaSession, noaWarn(noaSession))

	input := "Correlate the new findings and the current situation below into evidence-backed attack paths. Call propose_path for every chain the evidence supports, then validate_path_step / refute_path the steps of your open paths using the evidence already on record; propose nothing without real refs."
	if situational == "" {
		situational = "\n\n[No new findings and no open paths since your last round.]"
	}
	input += situational

	_, _, err := captureRun(ctx, opts, input, func(r db.Activity) {
		if emit != nil {
			r.Worker = "chain"
			emit(r)
		}
	})
	if err == nil && maxFindingID > 0 {
		// a lost cursor advance only re-shows findings next round — never fatal
		_ = ds.AdvanceChainCursor(taskID, maxFindingID)
	}
	return err
}
