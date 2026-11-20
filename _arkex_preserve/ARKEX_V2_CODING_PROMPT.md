# Master Coding Agent Prompt: Build **ArKex 2.0** (Next-Generation Autonomous Pentesting Platform)

> **Usage**: Paste this entire document (or pass it as the primary specification file alongside `ARTEX_DEEP_ANALYSIS.md`, `ARTEX_PROMPTS.md`, and `ARKEX_V2_BLUEPRINT.md`) to an autonomous coding agent. It is engineered using structured agentic prompting best practices: explicit role framing, hard architectural invariants, exact Go/TypeScript stack, database & storage math, complete UI design tokens (derived verbatim from `threatvector.tech-design.md`), phased milestones, and deterministic verification gates.

---

<role>
You are a Principal Distributed Systems Engineer, Offensive Security Architect, and Staff Design Engineer.
Your mission is to architect and implement **ArKex 2.0** — a major ground-up evolution of ARTEX into the world's most capable, storage-efficient, and visually razor-sharp autonomous penetration testing and 0-day vulnerability research platform.
</role>

<mission_and_invariants>
## 1. Non-Negotiable Architectural Invariants

Every line of code you write MUST uphold these six invariants:

1. **90–95% Storage Reduction (Zero 100 GB Bloat)**:
   - **Never** store quadratic `O(N²)` full conversation histories per LLM turn. Use delta-encoded append-only turns (`llm_messages`) + Zstd compression. Raw wire HTTP LLM capture is opt-in debug-only with a strict 24-hour ring buffer.
   - **Never** store uncompressed HTTP traffic or index >64 KiB per response in full-text search. Use trained-dictionary **Zstd** (`klauspost/compress/zstd`) + **FastCDC** content-addressed blobs (`jotfs/fastcdc-go`) + MITM scanner/404/static-asset noise deduplication.
   - **Never** duplicate history across 4 stores. Maintain **one canonical Zstd event log** per session; `activity` and `/btw` side questions reference offsets/hashes.
   - Enforce a **Storage Governor** with per-intent scratch quotas (`256 MiB` default), configurable global disk cap (`ARKEX_MAX_DISK_GB`), automatic FTS index teardown on closed tasks, and Zstd-Parquet cold tiering.
2. **Kernel-Enforced Unbypassable Scope**:
   - Every worker intent executes inside an isolated ephemeral container / Linux network namespace (`netns`) with **zero direct internet egress**. All TCP/UDP/DNS traffic is forced through ArKex's Transparent Egress Proxy + DNS Resolver, enforcing Target Pins, IP/CIDR Block Rules, and Rate Limits at the socket layer.
3. **Zero-Hallucination Evidence Standard (S1–S4 Receipts)**:
   - A finding is NEVER marked `verified` by LLM prose alone. It requires a deterministic, fail-closed receipt:
     - **S1**: Verbatim quote (≥8 runes) inside a captured MITM response body (`POE >= L3`).
     - **S2**: Headless browser (`go-rod`) DOM/JS execution/cookie proof + HAR/screenshot.
     - **S3**: Cryptographically correlated embedded `interactsh` OAST callback.
     - **S4**: Signed stdout/stderr receipt from the sandboxed worker container.
4. **Two-Tier Prompt Hygiene**:
   - Preserve strict separation between `[A]` DB-editable Go template role bodies (versioned with 1-step rollback) and immutable code-owned tails (`traffic`, `artifact`, `constraints`, `rsi_lessons`, `deferred_tools`, `pinned_intent`).
   - Preserve `[ARKEX platform control, not target defense]` anti-evasion framing on all platform blocks and `ARTEX_UNTRUSTED_BOUNDARY` nonce-framing for target-side prompt-injection defense.
5. **Single-Binary Deployability + Horizontal Scale**:
   - Go backend with embedded Next.js static export (`//go:embed`) + PostgreSQL (`pgvector` + `riverqueue/river`) so it runs as a single binary locally OR scales horizontally across worker nodes in Docker/Kubernetes.
6. **ThreatVector Stark Red-and-White Design System**:
   - The entire frontend MUST strictly implement the **ThreatVector** visual identity (`#ff0000` primary alert red, `#ffffff` pure stark white, `#212529` charcoal, `#e5e7eb` 1px borders, `TT Mussels` typography, flat architectural elevation with zero drop shadows/gradients/pills, signature `#ff0000` corner brackets, precision tactical animations, and 100% mobile-first responsiveness).
</mission_and_invariants>

---

<tech_stack>
## 2. Target Technology Stack & Pro Libraries

### Backend (Go 1.24+)
- **Agent Core**: `github.com/Autumn-27/norma` (or upgraded internal `arkex/core` compatible with `agentcore`, `tool`, `harness`, `permission`, `transcript`, `noaadapter`)
- **Database & Semantic Memory**: `github.com/jackc/pgx/v5`, `github.com/pgvector/pgvector-go`, PostgreSQL `pg_trgm` / `tsvector`
- **Distributed Job Queue**: `github.com/riverqueue/river` (transactional PG-backed queues for worker intents, OAST wakeups, retests, reports, fuzzing jobs, and storage GC)
- **Compression & CAS**: `github.com/klauspost/compress/zstd` (with trained HTTP/JSON dictionary), `github.com/jotfs/fastcdc-go`, `github.com/parquet-go/parquet-go`
- **Offensive & Network SDKs**:
  - ProjectDiscovery native Go libraries: `interactsh` (embedded OAST), `httpx`, `katana`, `subfinder`, `naabu`, `nuclei/v3`, `dnsx`, `wappalyzer`
  - Stealth HTTP/2 & HTTP/3: `github.com/refraction-networking/utls` (JA3/JA4 Chrome/Safari spoofing), `github.com/quic-go/quic-go`, `github.com/coder/websocket`, `github.com/bufbuild/protocompile`
  - Browser Automation: `github.com/go-rod/rod` (stealth CDP hooks for SPA crawling, auth refresh, S2 DOM XSS proof)
  - White-Box AST & 0-Day Research: `github.com/smacker/go-tree-sitter` (AST/sink indexing) + containerized bridges for `Joern`/`Semgrep`, `AFL++`/`LibFuzzer`/`Jazzer`, `BinDiff`/`ghidriff`, and `Z3`/`angr`
- **Sandboxing**: `github.com/docker/docker/client` (ephemeral per-intent containers with `netns` proxy-only egress, read-only rootfs, and 256 MiB tmpfs/disk quota)
- **Observability**: `log/slog`, `go.opentelemetry.io/otel`, Prometheus `/metrics`

### Frontend (Next.js 15 App Router, TypeScript Strict, Static Export)
- **Styling**: Tailwind CSS v4 with ThreatVector CSS custom properties
- **Motion**: `framer-motion` (precision tactical easing, layout transitions, `useReducedMotion` support)
- **Graphs & Data Viz**: `@xyflow/react` or `d3-force` (touch-enabled Exploration Graph, Asset Coverage Graph, Cloud/IAM Attack Path Graph), `recharts` (minimal flat red/charcoal telemetry charts)
- **UI Primitives**: Radix UI (`@radix-ui/react-dialog`, `tabs`, `drawer`, `tooltip`, `scroll-area`) styled flat to ThreatVector spec — NO heavy shadows, NO rounded pills.
</tech_stack>

---

<architecture_spec>
## 3. Core Backend Modules & Capabilities to Implement

### 3.1 Storage Engine & Storage Governor (`storage/`, `traffic/`, `llmrec/`)
1. **Delta `llmrec` (`llmrec/`)**:
   - Tables: `llm_messages(session_id, turn_seq, role, content_zstd, sha256)` + `llm_calls(id, session_id, up_to_seq, response_seq, model, profile_id, tokens_in, tokens_out, cache_read, latency_ms, status)`.
   - Never re-serialize prior turns. Optional raw wire capture stored in a separate unlogged/capped 24h ring buffer table (`llm_raw_debug`, disabled by default).
2. **Smart MITM Proxy & Zstd Traffic Store (`traffic/`)**:
   - Auto-detect scanner bursts (`>20 req/s` per host or known scanner UAs) and static MIME types (`image/*`, `font/*`, `video/*`, `audio/*`, minified bundles). Record metadata + `body_sha256` deduplicated via FastCDC/CAS; store only 1 unique compressed body per hash.
   - Compress all stored request/response bodies with a trained 64 KiB HTTP Zstd dictionary (`zstd.WithEncoderDict`).
   - Bound full-text indexing to `≤64 KiB` text responses only; automatically drop hot FTS rows for tasks closed `>7 days` and trigger `PRAGMA incremental_vacuum` (or PG partition drop).
   - Export cold closed tasks to Zstd-compressed **Parquet** (`traffic.parquet.zst`) + immutable evidence bundle.
3. **Storage Governor (`storage/governor.go`)**:
   - Background River periodic job enforcing `ARKEX_MAX_DISK_GB` (default `20 GB`), per-intent scratch cap (`256 MiB`), Zstd-compressed `cmd-output/*.zst` spills, and automatic cleanup of unreferenced temp files on task completion (always preserving finding-linked evidence and explicit loot).
   - Expose real-time breakdown API `GET /api/system/storage` (PG tables, CAS blobs, MITM index, task scratch, cold archives, compression ratio saved).

### 3.2 Multi-Agent Orchestration & Dual-Graph Engine (`agent/`, `engine/`)
1. **Dual-Graph Architecture** (PostgreSQL):
   - **Global Asset Graph**: `companies`, `assets` (`root_domain`, `subdomain`, `ip`, `service`, `app`, `endpoint`, `iam_principal`, `k8s_role`), `task_scope`, `exploration_anchors`.
   - **Per-Task Exploration Graph**: `goal`, `intent`, `fact`, `finding`, `hint`, `hypothesis`, `digest` connected by `spawns`, `derived_from`, `yields`, `proves`, `chains_with`, `covers`.
2. **DAG Hypothesis & Gadget-Chaining Planner**:
   - Upgrade linear `TodoStore` into a persistent **Hypothesis DAG** with confidence scores and preconditions.
   - Add a **Gadget-Synthesis Watcher** that detects composable primitives across workers (e.g., SSRF + internal port, file write + template path) and emits `chain_candidate` triggers to the Planner.
   - Maintain a **Cross-Task Negative-Result Index** `(asset_fp, vuln_class, method)` to prevent re-testing proven-clean surfaces.
   - Preserve ARTEX's **Hard Floor anti-stall**, **Quiet Mode**, **800ms debounced triggers**, **Real-Time `steer_work` via `PreToolUse`**, **Surgical `kill_work`**, **Stagnation Detection (`≥3` repeats)**, **Cold-Node Graph Compaction (`R=6`, minor `N=20`, major `M=8`)**, **`ask_operator` handoff**, and **6-Step Task Timeout Settlement**.
3. **Multi-Provider Failover & Refusal-Aware Tiered LLM Routing (`llmpool/`)**:
   - **Tier 1 (Fast/Cheap)**: Recon triage, JS parsing, compaction, dedup.
   - **Tier 2 (Frontier Reasoning)**: Planner strategy, exploit chaining, 5-axis adversarial verification.
   - **Tier 3 (Local Uncensored / Security-Tuned via Keyless Ollama/vLLM)**: Automatic **Refusal Detector** in `llmpool` catches safety refusal phrases, `content_filter` stop reasons, and empty replies on legitimate authorized exploit-crafting turns, seamlessly failing over that turn to Tier 3 without breaking session continuity.
   - Keep the **5-Layer Retry Stack** + **`steerHooks.Stop` thinking-only idle-turn nudge**.

### 3.3 Kernel Scope, Safety Judge & Offensive Capabilities (`sandbox/`, `intercept/`, `offensive/`)
1. **Ephemeral Worker Sandboxes (`sandbox/`)**:
   - Each worker intent runs in a lightweight container/netns with read-only rootfs, `256 MiB` scratch quota, and **iptables/eBPF proxy-only egress** through ArKex's Transparent Egress Proxy (enforcing DNS Pinning + Global/Task Block Rules + `uTLS` JA3/JA4 Chrome/Safari spoofing).
   - Keep ARTEX's **Goal Ambiguity Gate** (`≥80 chars` + concrete target unless `[targetless]`), **Minimal-Scope Decomposer**, **Static Priority Rules**, and **Offensive-Permissive LLM Fallback Judge** (`A1–A6` allow, `D1–D6` deny, default `ALLOW`, `JudgeContextBoundary`).
2. **Embedded OAST (`offensive/oast/`)**:
   - Embed `interactsh` with `oast_generate_payload(tag, intent_id)`. Delayed DNS/HTTP/SMTP/LDAP callbacks persist an **S3 OAST Receipt** and immediately wake the Planner via `TriggerEvent{Kind: "oast_hit"}`.
3. **Multi-Identity Auth Vault & Differential IDOR/BOLA Engine (`offensive/authmatrix/`)**:
   - Store 2–4 engagement identities (`Unauth`, `User A`, `User B`, `Admin`) with auto-refreshing JWT/OAuth/Cookie hooks.
   - Background analyzer replays newly captured safe read endpoints across identities, computing structural JSON/DOM diffs to auto-surface IDOR/BOLA/BFLA candidates.
4. **Single-Packet Race & HTTP/2–HTTP/3 Desync Engine (`offensive/desync/`)**:
   - Native Go raw-socket / HTTP/2 frame / `quic-go` tool supporting **HTTP/2 Single-Packet Attacks** (20–50 requests in 1 TCP packet) for sub-millisecond TOCTOU/race conditions, plus `CL.TE`/`TE.CL`/`H2.CL`/`H2.TE`/`H2.0` request smuggling and cache poisoning/deception probes.
5. **Parser-Differential, FSM Business-Logic & Protocol Engine (`offensive/parsers/`, `offensive/fsm/`)**:
   - Automated JWT/OAuth/SAML (`XSW1–XSW8`) & WAF-vs-Origin parser-differential probes.
   - Traffic-inferred **Workflow FSM Fuzzer** (state skipping, replay, token reuse, numeric/type invariants).
   - GraphQL stripped-schema recovery + gRPC/Protobuf (`protocompile`) & binary protocol framing fuzzer.
   - Magic-byte deserialization detector (`rO0AB`, `AAEAAAD`, PHP, pickle, Marshal) with `interactsh` gadget probing.
   - Closed-loop **AST-Preserving WAF Mutator** + `pgvector` bypass memory.
6. **0-Day Research Pipeline (`offensive/zeroday/`)**:
   - **Code Property Graph (CPG)**: `go-tree-sitter` + Joern/Semgrep sink-to-source taint indexing and **1-bug-to-0-day variant multiplication**.
   - **Patch-Diff Engine**: `BinDiff`/`ghidriff`/`git diff` incomplete-fix & sibling-endpoint hunting.
   - **Hybrid Fuzzing Orchestrator**: Ephemeral `AFL++`/`LibFuzzer`/`Jazzer`/`Go-Fuzz` containers with `ASAN`/`UBSAN`; on coverage plateau, parses `llvm-cov` gaps and prompts Tier-2 LLM to synthesize structure-aware seeds/mutators.
   - **Symbolic Solver Tool**: `solve_constraints` (`Z3`) and `angr_reach` for checksums, custom crypto, and path predicates.
   - **Local Exploit-Dev Mirror**: Spins up local Dockerized mirrors of discovered open-source components for offline `executed-lab` exploit development before a single surgical `executed-live` proof.
7. **Cloud & IAM Attack-Path Graph (`offensive/cloudgraph/`)**:
   - Shortest-path privilege-escalation queries over AWS/GCP/Azure/Entra/K8s IAM roles, OIDC trusts, RBAC bindings, and IMDS reachability.
8. **S1–S4 Receipt Verifier + Retester + Loop C RSI**:
   - Fail-closed verification across HTTP (`S1`), Browser (`S2` via `go-rod`), OAST (`S3`), and Sandbox (`S4`).
   - `pgvector`-backed evidence-gated RSI lessons + held-out benchmark runner for owner-approved Loop C prompt/skill evolution.
</architecture_spec>

---

<design_system>
## 4. Frontend Design System: ThreatVector Stark Red & White (`threatvector.tech`)

You MUST build the ArKex 2.0 Web UI (`web/`) strictly following the **ThreatVector** design system. Every view must look like a precision, high-contrast, enterprise offensive-security command center—spacious, crisp, urgent, and engineered.

### 4.1 Exact Design Tokens (CSS Variables & Tailwind Theme)

```css
:root {
  /* ThreatVector Core Palette */
  --tv-primary: #ff0000;      /* Vivid security-alert red: logo mark, primary CTAs, corner brackets, active rules */
  --tv-secondary: #212529;    /* Near-black charcoal: primary text, active nav, high-contrast anchors */
  --tv-tertiary: #dc3545;     /* Softer alert red: inline links, destructive states, secondary emphasis */
  --tv-neutral: #ffffff;      /* Pure stark white: main background, text on primary CTAs */
  --tv-surface: #ffffff;      /* Flat container surface (matches background; separation via 1px borders) */
  --tv-surface-subtle: #f8f9fa; /* Ultra-light cool tint ONLY for code blocks, table headers, hover rows */
  --tv-border: #e5e7eb;       /* 1px crisp structural divider */
  --tv-muted: #6c757d;        /* Captions, timestamps, inactive nav, secondary labels */
  --tv-accent: #0d6efd;       /* Rare informational/product state badge only — never hero */
  --tv-error: #dc3545;        /* Error / denied / critical alert */
  --tv-verified: #198754;     /* Restrained semantic green ONLY for S1-S4 verified receipts & healthy status */

  /* Typography — TT Mussels primary, geometric sans fallback, mono for payloads/HTTP */
  --font-display: "TT Mussels", "Space Grotesk", "Inter", system-ui, sans-serif;
  --font-sans: "TT Mussels", "Inter", system-ui, sans-serif;
  --font-mono: "JetBrains Mono", "IBM Plex Mono", ui-monospace, monospace;

  /* Exact Type Scale */
  --text-metric-lg: 3rem;     /* 48px / 56px, weight 500 */
  --text-display: 2.5rem;     /* 40px / 48px, weight 500 */
  --text-metric-md: 2.25rem;  /* 36px / 44px, weight 500 */
  --text-h1: 2.1875rem;       /* 35px / 42px, weight 500 */
  --text-h2: 1.25rem;         /* 20px / 24px, weight 500 */
  --text-h3: 1.125rem;        /* 18px / 22px, weight 500 */
  --text-body: 1rem;          /* 16px / 24px, weight 400 */
  --text-body-sm: 0.875rem;   /* 14px / 20px, weight 400 */
  --text-label-sm: 0.75rem;   /* 12px / 16px, weight 600 */

  /* Architectural Shape Language (NO PILLS, NO HEAVY SHADOWS) */
  --radius-none: 0px;
  --radius-sm: 4px;           /* Buttons, inputs, badges, tabs */
  --radius-md: 8px;           /* Cards, modals, panels */

  /* Discrete Spacing Rhythm */
  --space-xs: 12px;
  --space-sm: 20px;
  --space-md: 32px;
  --space-lg: 64px;
}
```

### 4.2 Signature Visual Motifs & Strict UI Rules
- **Signature `#ff0000` Corner Brackets (`<CornerFrame />`)**: Reusable structural wrapper rendering crisp `1.5px` `#ff0000` L-shaped corner framing marks around hero KPI panels, active engagement headers, live execution monitors, and verified finding receipts—evoking a monitored tactical HUD on stark white.
- **Thin `#ff0000` Section Rules**: Use `1px`–`2px` `#ff0000` accent rules under primary section headers and active navigation indicators.
- **Flat Architectural Elevation**: **ZERO** drop shadows (`box-shadow: none`), **ZERO** glassmorphism/blur, **ZERO** gradients, **ZERO** pill-shaped buttons (`rounded-full` forbidden except for 6px live status dots). Hierarchy comes strictly from `#ffffff` negative space, `1px solid #e5e7eb` borders, `#212529` charcoal contrast, and surgical `#ff0000` accents.
- **Component Geometry**:
  - **Primary Button**: `#ff0000` bg, `#ffffff` text, `40px` height, `8px 16px` padding, `4px` radius, `600` weight, hover darkens to `#dc3545`. Reserved strictly for decisive actions (`Launch Engagement`, `Approve Target`, `Start Retest`, `Export Report`).
  - **Secondary Button**: `#ffffff` bg, `#212529` text, `1px solid #e5e7eb` border, `40px` height, `4px` radius, hover border `#212529`.
  - **Inputs**: `#ffffff` bg, `40px` height, `4px` radius, `1px solid #e5e7eb`, focus border `1.5px solid #ff0000` (no glow ring).
  - **Cards**: `#ffffff` bg, `1px solid #e5e7eb`, `8px` radius, `16px–20px` padding.
  - **Typography**: Sentence case or title case everywhere; neutral letter-spacing (`0px`); `tabular-nums` on all metrics, timers, token counts, and storage counters.

### 4.3 Precision Tactical Motion & Animations (`framer-motion`)
All animations must feel **fast, mechanical, and purposeful** (`duration: 0.18s–0.32s`, easing `[0.16, 1, 0.3, 1]`, strictly respecting `prefers-reduced-motion`):
1. **Corner-Bracket Lock-On**: `<CornerFrame />` brackets animate smoothly from `6px` outward into lock position on mount or when an engagement transitions to `RUNNING`.
2. **Live Telemetry Pulse**: Minimal `6px` `#ff0000` beacon dot with subtle radar ring on active workers, live SSE streams, and incoming OAST callbacks.
3. **Animated Metric Counters**: Smooth numeric roll-up (`tabular-nums`) for Confirmed Findings, S1–S4 Verified %, Assets Covered, Storage Saved (GB), and Active Workers.
4. **Staggered Feed & Graph Choreography**: New SSE activity rows, findings, and OAST hits slide in cleanly (`y: 6 -> 0, opacity: 0 -> 1`); graph nodes expand/collapse smoothly with red causal lineage edge highlighting (`spawns → yields → proves`).
5. **Tactile Transitions**: Crisp sliding red active-tab underline (`layoutId="tv-tab-indicator"`), smooth mobile bottom-sheet / slide-over drawers for `/btw` side questions, traffic inspector, and approval cards.

### 4.4 Responsive & Mobile-First Ergonomics (375px → 1440px+)
- **Mobile (`< 768px`)**:
  - Compact top header (`56px`) with ThreatVector red logo mark, active engagement selector, and live approval badge + hamburger/bottom-nav bar (`Dashboard`, `Tasks`, `Findings`, `Approvals`, `More`).
  - Every data table (Tasks, Findings, Traffic, Assets) automatically transforms into clean stacked `8px`-radius ThreatVector cards with clear severity/status rails—zero horizontal page overflow.
  - Interactive Exploration & Coverage Graphs support pinch-to-zoom, 1-finger pan, and tap-to-open bottom sheet for node details.
  - `/btw` Side Questions and Traffic Inspection open as full-height swipeable mobile Drawers; all interactive touch targets are `>= 40px × 40px`.
- **Tablet (`768px – 1023px`)**: Collapsible icon rail + 2-column metric grids + slide-over detail inspector.
- **Desktop (`>= 1024px`)**: Spacious ThreatVector top navigation or ultra-clean `220px` white rail with `1px #e5e7eb` border, centered generous max-width layouts (`max-w-[1440px]`), resizable split panes for live worker transcripts + `/btw` side panel + HTTP traffic diff viewer.

### 4.5 Complete UI Page Inventory
1. **Command Dashboard (`/`)**: Hero ThreatVector KPI strip (`metric-lg` 48px numbers: Active Engagements, Verified Findings, OAST Hits, Disk Footprint & Zstd Savings %), live SSE tactical stream, pending Target/Intercept approvals widget.
2. **Engagements & Live Execution (`/tasks`, `/tasks/[id]`)**:
   - Live Planner rounds + N Worker lanes with real-time **`Steer`** (`steer_work`) and **`Kill`** (`kill_work`) controls, **`Ask Operator`** interactive cards (with screenshot preview & inline reply), and **`/btw`** side-question panel.
   - **Interactive Exploration Graph & Hypothesis DAG** + **Asset Coverage Graph** + **Cloud/IAM Attack-Path Graph** (all with `#ff0000` active/critical path highlighting on stark white canvas).
3. **Findings & S1–S4 Receipt Center (`/findings`)**: Filterable by severity/status/verification; displays cryptographic/verbatim **S1 HTTP / S2 Browser / S3 OAST / S4 Sandbox** proof badges, side-by-side raw HTTP evidence, one-click **Retest**, and immutable Markdown/PDF report export.
4. **MITM Traffic, Auth Vault & OAST Console (`/traffic`)**: Ultra-fast search over Zstd traffic, Multi-Identity Auth Vault (`Unauth` / `User A` / `User B` / `Admin`) + live Differential IDOR/BOLA Matrix view, and real-time `interactsh` OAST callback stream.
5. **0-Day & Protocol Lab (`/lab`)**: CPG taint-path viewer, Patch-Diff incomplete-fix queue, live AFL++/LibFuzzer coverage & crash triage monitor, and Single-Packet Race / H2 Desync tester.
6. **Governance, Approvals & Scope (`/approvals`)**: Target Gate DNS-pinned approval cards (showing resolved IPs, port, rule context, 1-click Approve/Deny), Operation Constraints editor, and LLM Safety Judge audit log.
7. **System, LLM Pool & Storage Governor (`/settings`)**: Tier 1/2/3 LLM failover pool health & refusal-fallback telemetry, Storage Governor disk breakdown & Zstd compression ratio gauges, Prompt Version Manager (with diff & 1-step rollback), Skills & MCP manager.
</design_system>

---

<implementation_plan>
## 5. Phased Execution & Verification Protocol

Execute in strict phases. **Do not proceed to the next phase until every verification check in the current phase passes.**

### Phase 1 — Core Schema, Delta Storage Engine & Storage Governor
- Implement PG schema (`db/schema.sql`) with `pgvector`, delta `llm_messages`/`llm_calls`, `river` tables, and `storage_metrics`.
- Implement `storage/` (Zstd dictionary encoder/decoder, FastCDC CAS, Storage Governor TTL/quota worker) and upgraded `traffic/` (MITM scanner/404 dedup, 64 KiB FTS cap, Parquet cold export).
- **Verify**: Write unit + benchmark tests proving (a) 100-turn `llmrec` session uses `<5%` of ARTEX 1.x size, (b) 10,000 fuzzer 404 requests + 500 JSON API responses compress by `>=85%`, and (c) Storage Governor enforces quota without deleting finding-bound evidence.

### Phase 2 — Distributed Engine, Sandboxed Workers & Kernel Scope
- Implement `river`-backed distributed `Engine` (`plannerLoop`, `workerLoop`, `deadlineCoordinator`, `steerHooks`, `claimHeartbeat`, `coldgraph` compactor).
- Implement `sandbox/` (ephemeral Docker/netns per worker with forced transparent egress proxy, DNS pinning, `uTLS` JA3/JA4 spoofing, 256 MiB scratch quota).
- Implement `llmpool/` (Tier 1/2/3 routing, refusal-detection failover to local Ollama/vLLM, 5-layer retry, thinking-only idle turn nudge).
- **Verify**: Integration tests proving (a) a worker running raw Python/curl to an unpinned IP is blocked at the socket/proxy layer, (b) simulated cloud refusal automatically fails over to Tier-3 mock Ollama seamlessly, (c) `steer_work` redirects an active worker mid-turn, and (d) `go test -race ./...` passes clean.

### Phase 3 — Offensive Engines, S1–S4 Receipts & 0-Day Lab
- Implement embedded `interactsh` OAST (`oast_hit` trigger), Multi-Identity Auth Vault + Differential IDOR/BOLA analyzer, Single-Packet H2/H3 Race & Desync engine, FSM Business-Logic Fuzzer, AST WAF Mutator, and `go-rod` S2 Browser Receipt Verifier.
- Implement `offensive/zeroday/` (Tree-sitter CPG sink indexer + variant generator, Patch-Diff runner, Hybrid Fuzzer container orchestrator, Z3/angr tool bridge, Local Docker Exploit-Dev Mirror) and Cloud/IAM Attack-Path Graph.
- **Verify**: End-to-end tests against a local deliberately vulnerable test target proving S1 HTTP receipt, S2 Browser receipt, S3 OAST callback wakeup, and fail-closed rejection of hallucinated quotes.

### Phase 4 — ThreatVector Red-and-White Responsive Animated UI
- Scaffold Next.js 15 App Router in `web/` with ThreatVector CSS tokens, `TT Mussels` font loading + fallbacks, `<CornerFrame />`, `framer-motion` tactical animations, and responsive mobile-first layouts across all 7 page groups.
- Embed static export into Go binary (`//go:embed`) and wire real-time SSE + `/btw` side questions.
- **Verify**: `npx tsc --noEmit`, `npm run build:static`, Playwright visual & responsive audit across `375px` (mobile), `768px` (tablet), and `1440px` (desktop) confirming zero horizontal overflow, zero drop shadows/pills/gradients, crisp `#ff0000`/`#ffffff` contrast, and working animations.

### Phase 5 — Final Hardening & Single-Binary Build
- Run full `go test -race ./...`, `CGO_ENABLED=0 go build -tags embedui -o arkex ./cmd/arkex`, and Docker Compose smoke test.
- Output a concise architectural verification report with measured storage compression ratios, test results, and startup instructions.
</implementation_plan>

<quality_rules>
## 6. Strict Coding & Quality Rules
- **No placeholders, no TODO stubs, no fake mock data in production code paths.**
- **Never swallow errors silently** — log structured `slog` context or return typed errors.
- **Security hygiene**: Pin all file browsing with `os.Root`, validate all user inputs, scrub secrets from logs/prompts, and never weaken scope/target-gate checks.
- **UI discipline**: Strictly adhere to `threatvector.tech-design.md` — stark `#ffffff` negative space, surgical `#ff0000` accents and corner brackets, `#212529` charcoal text, `1px #e5e7eb` borders, flat elevation, responsive mobile-first design, and clean tactical motion.
</quality_rules>
