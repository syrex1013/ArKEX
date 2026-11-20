// Package zeroday provides CPG sink indexing, patch-diff heuristics, and fuzz job records.
package zeroday

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Sink is one taint sink candidate from source indexing.
type Sink struct {
	FilePath   string
	LineNo     int
	Kind       string
	SourceLine string
	Confidence float32
}

// PatchFinding is an incomplete-fix heuristic hit from a unified diff.
type PatchFinding struct {
	File    string
	Summary string
	Kind    string
}

// FuzzSpec describes a fuzzer launch request.
type FuzzSpec struct {
	Kind       string // afl++, libfuzzer, jazzer, go-fuzz
	TargetKind string
	Target     string
	Image      string
}

// Pipeline orchestrates lab research jobs against PostgreSQL.
type Pipeline struct {
	db      *sql.DB
	workDir string
}

func New(db *sql.DB, workDir string) *Pipeline {
	return &Pipeline{db: db, workDir: workDir}
}

var sinkPatterns = []struct {
	kind  string
	re    *regexp.Regexp
	conf  float32
	langs []string
}{
	{"sqli", regexp.MustCompile(`(?i)(query|exec|execute|rawquery|createquery)\s*\(.*(\+|fmt\.Sprintf|\$\{)`), 0.7, []string{"go", "js", "ts", "java", "php", "py"}},
	{"cmdi", regexp.MustCompile(`(?i)(exec\.Command|os\.system|subprocess|Runtime\.exec|child_process|popen)\s*\(`), 0.75, []string{"go", "js", "ts", "java", "py", "php"}},
	{"xss", regexp.MustCompile(`(?i)(innerHTML|dangerouslySetInnerHTML|document\.write|v-html)\s*[=:(]`), 0.65, []string{"js", "ts", "jsx", "tsx", "vue"}},
	{"path", regexp.MustCompile(`(?i)(open|readfile|writefile|ioutil\.read|os\.open|include|require)\s*\(.*(\+|filepath\.join|path\.join|\$\{)`), 0.6, []string{"go", "js", "ts", "py", "php", "java"}},
	{"ssrf", regexp.MustCompile(`(?i)(http\.(get|post|client)|axios\.|fetch\(|requests\.(get|post)|urllib)\s*\(`), 0.45, []string{"go", "js", "ts", "py"}},
	{"deser", regexp.MustCompile(`(?i)(pickle\.loads|yaml\.load\(|unserialize\(|ObjectInputStream|BinaryFormatter)`), 0.8, []string{"py", "php", "java", "cs"}},
}

// IndexSource walks repoPath and persists sink hits for taskID.
func (p *Pipeline) IndexSource(ctx context.Context, taskID, repoPath, language string) ([]*Sink, error) {
	info, err := os.Stat(repoPath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("repo path is not a directory")
	}
	language = strings.ToLower(strings.TrimSpace(language))
	var sinks []*Sink
	err = filepath.WalkDir(repoPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" || name == "dist" || name == ".next" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.TrimPrefix(filepath.Ext(path), ".")
		if language != "" && language != "auto" && !langMatch(language, ext) {
			return nil
		}
		fileSinks, err := scanFile(path, ext)
		if err != nil {
			return nil
		}
		sinks = append(sinks, fileSinks...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, s := range sinks {
		if p.db == nil {
			continue
		}
		_, err := p.db.ExecContext(ctx, `
INSERT INTO cpg_sinks (task_id, file_path, line_no, sink_kind, source_line, language, confidence)
VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			taskID, s.FilePath, s.LineNo, s.Kind, s.SourceLine, language, s.Confidence)
		if err != nil {
			slog.Warn("cpg persist sink", "err", err)
		}
	}
	slog.Info("cpg indexed", "sinks", len(sinks), "task", taskID)
	return sinks, nil
}

func scanFile(path, ext string) ([]*Sink, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []*Sink
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		for _, pat := range sinkPatterns {
			if !extAllowed(pat.langs, ext) {
				continue
			}
			if pat.re.MatchString(line) {
				out = append(out, &Sink{
					FilePath:   path,
					LineNo:     lineNo,
					Kind:       pat.kind,
					SourceLine: trimLine(line),
					Confidence: pat.conf,
				})
			}
		}
	}
	return out, sc.Err()
}

// PatchDiff runs git diff and flags incomplete-fix patterns.
func (p *Pipeline) PatchDiff(ctx context.Context, beforeRef, afterRef, repoPath string) ([]*PatchFinding, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "diff", beforeRef+".."+afterRef, "--unified=3")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	return analyzePatchDiff(string(out)), nil
}

func analyzePatchDiff(diff string) []*PatchFinding {
	var findings []*PatchFinding
	var file string
	lines := strings.Split(diff, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "+++ b/") {
			file = strings.TrimPrefix(line, "+++ b/")
			continue
		}
		if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
			continue
		}
		added := strings.TrimPrefix(line, "+")
		// Incomplete fix: added validation without removing dangerous sink nearby
		if regexp.MustCompile(`(?i)(sanitize|escape|allowlist|blocklist|validate)`).MatchString(added) {
			window := strings.Join(lines[max(0, i-8):min(len(lines), i+8)], "\n")
			if regexp.MustCompile(`(?i)(exec\.Command|innerHTML|pickle\.loads|query\()`).MatchString(window) {
				findings = append(findings, &PatchFinding{
					File:    file,
					Kind:    "incomplete_fix",
					Summary: "validation added near residual sink — sibling path may remain vulnerable",
				})
			}
		}
		if regexp.MustCompile(`(?i)TODO|FIXME|HACK`).MatchString(added) {
			findings = append(findings, &PatchFinding{
				File:    file,
				Kind:    "todo_in_patch",
				Summary: "patch introduces TODO/FIXME — treat as incomplete remediation",
			})
		}
	}
	return findings
}

// LaunchFuzzer records a fuzz job; container launch is best-effort when Docker exists.
func (p *Pipeline) LaunchFuzzer(ctx context.Context, taskID string, spec *FuzzSpec) (int64, error) {
	if spec == nil {
		return 0, fmt.Errorf("nil fuzz spec")
	}
	var id int64
	err := p.db.QueryRowContext(ctx, `
INSERT INTO fuzz_jobs (task_id, target_kind, target_ref, fuzzer, status)
VALUES ($1,$2,$3,$4,'queued') RETURNING id`,
		taskID, spec.TargetKind, spec.Target, spec.Kind).Scan(&id)
	if err != nil {
		return 0, err
	}
	image := spec.Image
	if image == "" {
		image = "aflplusplus/aflplusplus:latest"
	}
	if _, err := exec.LookPath("docker"); err != nil {
		_, _ = p.db.ExecContext(ctx, `UPDATE fuzz_jobs SET status = 'queued_no_docker', updated_at = now() WHERE id = $1`, id)
		return id, nil
	}
	name := fmt.Sprintf("arkex-fuzz-%d", id)
	cmd := exec.CommandContext(ctx, "docker", "run", "-d", "--name", name, "--rm",
		"--memory=512m", "--cpus=1", image, "sleep", "3600")
	out, err := cmd.Output()
	if err != nil {
		_, _ = p.db.ExecContext(ctx, `UPDATE fuzz_jobs SET status = 'failed', updated_at = now() WHERE id = $1`, id)
		return id, fmt.Errorf("docker run: %w", err)
	}
	cid := strings.TrimSpace(string(out))
	_, _ = p.db.ExecContext(ctx, `UPDATE fuzz_jobs SET status = 'running', container_id = $2, updated_at = now() WHERE id = $1`, id, cid)
	return id, nil
}

// ListSinks returns recent CPG sinks for a task.
func (p *Pipeline) ListSinks(ctx context.Context, taskID string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := p.db.QueryContext(ctx, `
SELECT file_path, line_no, sink_kind, source_line, language, confidence
FROM cpg_sinks WHERE task_id = $1 ORDER BY id DESC LIMIT $2`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var fp, kind, src, lang string
		var line int
		var conf float32
		if err := rows.Scan(&fp, &line, &kind, &src, &lang, &conf); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"file_path": fp, "line_no": line, "sink_kind": kind,
			"source_line": src, "language": lang, "confidence": conf,
		})
	}
	return out, rows.Err()
}

func langMatch(language, ext string) bool {
	switch language {
	case "go":
		return ext == "go"
	case "js", "javascript":
		return ext == "js" || ext == "jsx" || ext == "mjs"
	case "ts", "typescript":
		return ext == "ts" || ext == "tsx"
	case "py", "python":
		return ext == "py"
	case "java":
		return ext == "java"
	case "php":
		return ext == "php"
	default:
		return true
	}
}

func extAllowed(langs []string, ext string) bool {
	for _, l := range langs {
		if langMatch(l, ext) {
			return true
		}
	}
	return false
}

func trimLine(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 240 {
		return s[:240]
	}
	return s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
