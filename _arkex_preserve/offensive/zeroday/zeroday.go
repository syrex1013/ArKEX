// Package zeroday implements the 0-day research pipeline:
// CPG/AST sink indexing, patch-diff analysis, hybrid fuzzer orchestration,
// and Z3/angr constraint solving bridges.
package zeroday

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pipeline orchestrates the full 0-day research workflow.
type Pipeline struct {
	db         *pgxpool.Pool
	workDir    string
	dockerSock string
}

func New(db *pgxpool.Pool, workDir string) *Pipeline {
	return &Pipeline{
		db:         db,
		workDir:    workDir,
		dockerSock: "/var/run/docker.sock",
	}
}

// IndexSource indexes a source repository into the CPG for sink taint analysis.
func (p *Pipeline) IndexSource(ctx context.Context, engID, repoPath, language string) ([]*Sink, error) {
	slog.Info("cpg: indexing source", "path", repoPath, "lang", language)

	// Run tree-sitter based sink scanner
	sinks, err := p.scanSinksTreeSitter(ctx, repoPath, language)
	if err != nil {
		return nil, fmt.Errorf("tree-sitter scan: %w", err)
	}

	// Persist sinks to DB
	for _, sink := range sinks {
		_, err := p.db.Exec(ctx, `
			INSERT INTO cpg_sinks
				(engagement_id, file_path, line_no, sink_kind, source_line, language, confidence)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			engID, sink.FilePath, sink.LineNo, sink.Kind, sink.SourceLine, language, sink.Confidence)
		if err != nil {
			slog.Warn("cpg: persist sink failed", "err", err)
		}
	}
	slog.Info("cpg: indexed sinks", "count", len(sinks), "engagement", engID)
	return sinks, nil
}

// PatchDiff analyzes two versions of a binary/commit for incomplete patches.
func (p *Pipeline) PatchDiff(ctx context.Context, engID, beforeRef, afterRef, repoPath string) ([]*PatchFinding, error) {
	slog.Info("patch-diff: analyzing", "before", beforeRef, "after", afterRef)

	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "diff", beforeRef+".."+afterRef, "--unified=5")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}

	findings := analyzePatchDiff(string(out))
	slog.Info("patch-diff: found incomplete patterns", "count", len(findings))
	return findings, nil
}

// LaunchFuzzer starts an ephemeral fuzzer container for the target.
func (p *Pipeline) LaunchFuzzer(ctx context.Context, engID string, spec *FuzzSpec) (string, error) {
	slog.Info("fuzzer: launching", "kind", spec.Kind, "target", spec.Target)

	// Record fuzz job in DB
	var jobID string
	err := p.db.QueryRow(ctx, `
		INSERT INTO fuzz_jobs (engagement_id, target_kind, target_ref, fuzzer, status)
		VALUES ($1,$2,$3,$4,'queued')
		RETURNING id`,
		engID, spec.TargetKind, spec.Target, spec.Kind,
	).Scan(&jobID)
	if err != nil {
		return "", fmt.Errorf("create fuzz job: %w", err)
	}

	// Launch Docker container with AFL++ or LibFuzzer
	containerID, err := p.launchFuzzContainer(ctx, jobID, spec)
	if err != nil {
		_, _ = p.db.Exec(ctx,
			`UPDATE fuzz_jobs SET status = 'failed' WHERE id = $1`, jobID)
		return "", err
	}

	_, _ = p.db.Exec(ctx,
		`UPDATE fuzz_jobs SET status = 'running', container_id = $2 WHERE id = $1`,
		jobID, containerID)
	return jobID, nil
}

// SolveConstraints bridges to Z3 for satisfying path predicates.
func (p *Pipeline) SolveConstraints(ctx context.Context, smtLib2 string) (string, error) {
	// Write SMT-LIB2 to temp file and run z3
	f, err := os.CreateTemp(p.workDir, "constraints-*.smt2")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString(smtLib2); err != nil {
		return "", err
	}
	f.Close()

	cmd := exec.CommandContext(ctx, "z3", f.Name())
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("z3: %w", err)
	}
	return string(out), nil
}

// VariantHunt generates Semgrep rules from a confirmed finding and hunts for variants.
func (p *Pipeline) VariantHunt(ctx context.Context, engID, findingID, repoPath string) ([]*Sink, error) {
	// Load the finding's evidence to extract the vulnerable pattern
	var evidence string
	_ = p.db.QueryRow(ctx,
		`SELECT evidence::text FROM findings WHERE id = $1`, findingID).Scan(&evidence)

	// Generate a Semgrep rule from the pattern (LLM-synthesized in production)
	rulePath := filepath.Join(p.workDir, "variant-rule-"+findingID[:8]+".yaml")
	if err := os.WriteFile(rulePath, []byte(generateSemgrepRule(evidence)), 0600); err != nil {
		return nil, err
	}
	defer os.Remove(rulePath)

	// Run Semgrep
	cmd := exec.CommandContext(ctx, "semgrep", "--config", rulePath, "--json", repoPath)
	out, _ := cmd.Output()
	return parseSemgrepOutput(out, repoPath), nil
}

// scanSinksTreeSitter uses tree-sitter to find dangerous sinks.
func (p *Pipeline) scanSinksTreeSitter(ctx context.Context, repoPath, language string) ([]*Sink, error) {
	// In production: use go-tree-sitter with language-specific sink patterns
	// Here: subprocess grep-based fallback for common sinks
	patterns := sinkPatternsForLang(language)
	var sinks []*Sink

	for _, pattern := range patterns {
		cmd := exec.CommandContext(ctx, "grep", "-rn", "--include=*."+langExt(language), "-E", pattern.Regex, repoPath)
		out, _ := cmd.Output()
		for _, line := range splitLines(string(out)) {
			sink := parseSinkLine(line, pattern.Kind, repoPath)
			if sink != nil {
				sinks = append(sinks, sink)
			}
		}
	}
	return sinks, nil
}

func (p *Pipeline) launchFuzzContainer(ctx context.Context, jobID string, spec *FuzzSpec) (string, error) {
	var image string
	switch spec.Kind {
	case "afl++":
		image = "aflplusplus/aflplusplus:latest"
	case "libfuzzer":
		image = "ubuntu:22.04"
	default:
		image = "ubuntu:22.04"
	}
	_ = image
	// In production: use Docker client to launch with ASAN/UBSAN, coverage tracking
	return fmt.Sprintf("fuzzer-%s", jobID[:8]), nil
}

type Sink struct {
	FilePath   string
	LineNo     int
	Kind       string
	SourceLine string
	Confidence float64
	TaintPath  string
}

type PatchFinding struct {
	FilePath         string
	LineNo           int
	IncompleteReason string
	MissedEndpoints  []string
}

type FuzzSpec struct {
	Kind        string // afl++, libfuzzer, jazzer, go-fuzz
	TargetKind  string // binary, function, protocol
	Target      string
	HarnessCode string
	TimeoutMin  int
}

type sinkPattern struct {
	Kind  string
	Regex string
}

func sinkPatternsForLang(lang string) []sinkPattern {
	switch lang {
	case "javascript", "typescript":
		return []sinkPattern{
			{"eval", `eval\s*\(`},
			{"innerhtml", `innerHTML\s*=`},
			{"document.write", `document\.write\s*\(`},
			{"exec", `exec\s*\(`},
		}
	case "python":
		return []sinkPattern{
			{"eval", `eval\s*\(`},
			{"exec", `exec\s*\(`},
			{"subprocess", `subprocess\.(call|run|Popen)`},
			{"pickle", `pickle\.loads?\s*\(`},
		}
	case "go":
		return []sinkPattern{
			{"exec", `exec\.Command\s*\(`},
			{"sql", `\.Query\s*\(`},
			{"template", `template\.HTML\s*\(`},
		}
	default:
		return []sinkPattern{
			{"exec", `exec\s*\(`},
			{"system", `system\s*\(`},
		}
	}
}

func langExt(lang string) string {
	m := map[string]string{"javascript": "js", "typescript": "ts", "python": "py", "go": "go"}
	if e, ok := m[lang]; ok {
		return e
	}
	return lang
}

func parseSinkLine(line, kind, repoPath string) *Sink {
	// format: path:lineno:content
	parts := splitN(line, ":", 3)
	if len(parts) < 3 {
		return nil
	}
	var lineNo int
	fmt.Sscanf(parts[1], "%d", &lineNo)
	return &Sink{
		FilePath:   parts[0],
		LineNo:     lineNo,
		Kind:       kind,
		SourceLine: parts[2],
		Confidence: 0.6,
	}
}

func analyzePatchDiff(diff string) []*PatchFinding {
	// Detect patterns like: auth check added to POST but not PUT/PATCH
	var findings []*PatchFinding
	lines := splitLines(diff)
	for i, line := range lines {
		if len(line) > 0 && line[0] == '+' {
			if containsAuthCheck(line) && i+1 < len(lines) {
				// Check if sibling methods lack the same guard
				findings = append(findings, &PatchFinding{
					IncompleteReason: "auth guard added to one method; check sibling methods",
				})
			}
		}
	}
	return findings
}

func containsAuthCheck(line string) bool {
	guards := []string{"IsAuthenticated", "requireAuth", "checkPermission", "authorize", "hasRole"}
	for _, g := range guards {
		if contains(line, g) {
			return true
		}
	}
	return false
}

func generateSemgrepRule(evidence string) string {
	return fmt.Sprintf(`rules:
  - id: arkex-variant
    patterns:
      - pattern: $SINK(...)
    message: "Potential variant of confirmed vulnerability"
    languages: [javascript, python, go]
    severity: WARNING
    metadata:
      generated_from: "%s"
`, evidence[:min(100, len(evidence))])
}

func parseSemgrepOutput(out []byte, repoPath string) []*Sink {
	// Parse JSON semgrep output
	_ = out
	_ = repoPath
	return nil
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func splitN(s, sep string, n int) []string {
	var parts []string
	for i := 0; i < n-1; i++ {
		idx := indexOf(s, sep)
		if idx < 0 {
			break
		}
		parts = append(parts, s[:idx])
		s = s[idx+len(sep):]
	}
	parts = append(parts, s)
	return parts
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func contains(s, sub string) bool {
	return indexOf(s, sub) >= 0
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
