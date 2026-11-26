package server

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// rsiLessonsMode reads the shadow-mode switch:
//
//	off (default)  no selection, no logging, no prompt change
//	shadow         select lessons and log what would have been shown
//	inject         select, log, and append the block to planner/worker prompts
//
// The flag is read per task-bundle build, so changing it needs a new task or a
// task-agent invalidation, not a restart.
func rsiLessonsMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ARTEX_RSI_LESSONS"))) {
	case "shadow":
		return "shadow"
	case "inject":
		return "inject"
	default:
		return "off"
	}
}

// lessonsBlockFor selects the lessons visible to a task and renders the block.
// Returns (block, mode): mode "inject" means the caller should attach the
// block to the prompts, "shadow" selects and logs only, "" means off. Usage is
// logged in both modes so a later comparison can tell which tasks saw which
// lessons.
func (s *Server) lessonsBlockFor(t *Task) (string, string) {
	mode := rsiLessonsMode()
	if mode == "off" || t == nil {
		return "", mode
	}
	taskID, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil {
		return "", mode
	}
	lessons, err := s.m.PG().TaskLessons(context.Background(), taskID, 8)
	if err != nil {
		log.Printf("[rsi] lesson selection failed for task %s: %v", t.ID, err)
		return "", mode
	}
	block, ids := db.RenderLessons(lessons, 1800)
	if block == "" {
		return "", mode
	}
	if err := s.m.PG().LogLessonUsage(context.Background(), taskID, ids, mode); err != nil {
		log.Printf("[rsi] lesson usage log failed for task %s: %v", t.ID, err)
	}
	return block, mode
}

// taskLessons returns the lessons visible to one task plus the rendered block.
// Read-only: it never extracts.
func (s *Server) taskLessons(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "invalid task id")
		return
	}
	lessons, err := s.m.PG().TaskLessons(r.Context(), id, 50)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, 404, "task not found")
		return
	}
	if err != nil {
		log.Printf("[rsi] lesson list failed for task %d: %v", id, err)
		writeErr(w, 500, "lesson query failed")
		return
	}
	block, _ := db.RenderLessons(lessons, 1800)
	writeJSON(w, 200, map[string]any{
		"mode":    rsiLessonsMode(),
		"lessons": lessons,
		"block":   block,
		"count":   len(lessons),
	})
}

// extractTaskLessons derives candidate lessons from one task's findings and
// retests. It is idempotent; call it after the task closes (the manager does
// this automatically on terminal status) or manually for a backfill.
func (s *Server) extractTaskLessons(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "invalid task id")
		return
	}
	n, err := s.m.PG().ExtractLessonsForTask(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, 404, "task not found")
		return
	}
	if err != nil {
		log.Printf("[rsi] lesson extraction failed for task %d: %v", id, err)
		writeErr(w, 500, "lesson extraction failed")
		return
	}
	// Project knowledge extraction runs beside the lessons extract (idempotent
	// upserts, so re-running is safe). A failure never fails the request.
	if kn, err := s.m.PG().ExtractKnowledgeForTask(r.Context(), id); err != nil {
		log.Printf("[agent-lifecycle] task=%d agent=planner intent=0 kind=knowledge_extract FAILED: %v", id, err)
	} else {
		log.Printf("[agent-lifecycle] task=%d agent=planner intent=0 kind=knowledge_extract rows=%d", id, kn)
	}
	writeJSON(w, 200, map[string]any{"extracted": n})
}
