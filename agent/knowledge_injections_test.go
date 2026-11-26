package agent

import (
	"fmt"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

func TestLogKnowledgeInjections(t *testing.T) {
	d := testDB(t)
	defer d.Close()

	task, err := d.CreateTaskWithOptions("injection log task", "goal", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.DeleteTask(task.ID) }()

	cleanup := func() {
		_, _ = d.Exec(`DELETE FROM knowledge_items WHERE source_task_id=$1 AND kind='asset_fact' AND title LIKE 'injection log %'`, task.ID)
		_, _ = d.Exec(`DELETE FROM knowledge_injections WHERE task_id=$1`, task.ID)
	}
	cleanup()
	defer cleanup()

	ids := make([]int64, 0, 3)
	for i := range 3 {
		id, err := d.UpsertKnowledge(t.Context(), db.KnowledgeItem{
			Kind:         "asset_fact",
			Title:        fmt.Sprintf("injection log %d %d", task.ID, time.Now().UnixNano()+int64(i)),
			Body:         "seed body",
			SourceTaskID: &task.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	logKnowledgeInjections(d, task.ID, 77, "worker#0", "inject", ids)
	logKnowledgeInjections(d, task.ID, 0, "planner", "", ids[:1])

	rows, err := d.ListKnowledgeInjections(task.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("got %d injection rows, want 4 (3 worker + 1 planner): %+v", len(rows), rows)
	}
	// Newest-first: the planner call ran last, so it tops the list; the worker
	// rows follow in reverse item order.
	if rows[0].ItemID != ids[0] || rows[0].AgentName != "planner" || rows[0].Mode != "shadow" {
		t.Fatalf("planner row wrong: %+v", rows[0])
	}
	if rows[0].IntentID != nil {
		t.Errorf("planner row should have nil intent: %+v", rows[0])
	}
	wantWorker := []int64{ids[2], ids[1], ids[0]}
	for i, wantItem := range wantWorker {
		row := rows[1+i]
		if row.ItemID != wantItem || row.AgentName != "worker#0" || row.Mode != "inject" {
			t.Errorf("worker row %d wrong: %+v", 1+i, row)
		}
		if row.IntentID == nil || *row.IntentID != 77 {
			t.Errorf("worker row intent not recorded: %+v", row)
		}
	}
}

func TestLogKnowledgeInjectionsSkipsInvalidIDs(t *testing.T) {
	d := testDB(t)
	defer d.Close()

	task, err := d.CreateTaskWithOptions("injection skip task", "goal", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.DeleteTask(task.ID) }()
	defer func() {
		_, _ = d.Exec(`DELETE FROM knowledge_injections WHERE task_id=$1`, task.ID)
	}()

	// 0 and negative ids must be skipped without error; duplicate call is
	// best-effort (unknown item ids fail the FK and stop the loop silently).
	logKnowledgeInjections(d, task.ID, 0, "worker#0", "inject", []int64{0, -5})
	rows, err := d.ListKnowledgeInjections(task.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("invalid ids should log nothing, got %+v", rows)
	}
}
