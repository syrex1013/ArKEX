package agent

// Resume blocks: a re-run of a previously started intent gets its persisted
// checkpoint rendered into the launch input, so it continues from evidence
// instead of repeating finished work.

import (
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// ResumeState carries the resume decision made by the engine into the worker:
// which attempt this run is, the bound on attempts, and the latest persisted
// checkpoint (nil on a fresh run).
type ResumeState struct {
	Attempt, Max int
	Last         *db.IntentCheckpoint
}

// resumeBlockCap bounds the rendered block: a launch input is read every turn
// until compaction, so it stays small. When over, the open list trims first —
// redoing open work is cheaper than re-deriving finished work.
const resumeBlockCap = 1800

// SetResume wires the resume state for the next Execute. Called by the engine
// per runIntent, before worker.Execute.
func (w *Worker) SetResume(r ResumeState) { w.resume = r }

// renderResumeBlock renders the checkpoint into a launch-input block; empty
// when last is nil (nothing to resume from).
func renderResumeBlock(last *db.IntentCheckpoint, attempt, max int) string {
	if last == nil {
		return ""
	}
	done, open := last.Done, last.Open
	block := composeResumeBlock(last, attempt, max, done, open)
	for len(block) > resumeBlockCap && len(open) > 0 { // trim open first
		open = open[:len(open)-1]
		block = composeResumeBlock(last, attempt, max, done, open)
	}
	for len(block) > resumeBlockCap && len(done) > 0 {
		done = done[:len(done)-1]
		block = composeResumeBlock(last, attempt, max, done, open)
	}
	return trimRunes(block, resumeBlockCap)
}

// composeResumeBlock renders one variant of the block with the given lists.
func composeResumeBlock(last *db.IntentCheckpoint, attempt, max int, done, open []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nResume attempt %d/%d.\n", attempt, max)
	if last.Phase != "" {
		fmt.Fprintf(&b, "Last phase: %s\n", last.Phase)
	}
	if len(done) > 0 {
		b.WriteString("Done so far:\n")
		for _, item := range done {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	if len(open) > 0 {
		b.WriteString("Still open:\n")
		for _, item := range open {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	if len(last.Artifacts) > 0 {
		b.WriteString("Artifacts:\n")
		for _, item := range last.Artifacts {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	if last.NextStep != "" {
		fmt.Fprintf(&b, "Next step: %s\n", last.NextStep)
	}
	b.WriteString("Continue from the checkpoint; do not repeat completed steps.")
	return b.String()
}
