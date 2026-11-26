package server

import (
	"testing"

	"github.com/Autumn-27/artex/db"
)

// A specialized agent claims the intent that names its role before a role-less
// one, and a general agent claims a role-less intent.
func TestClaimRolePreference(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer m.Close()

	task, err := m.CreateTaskWithOptions("role claim", "role preference", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.DeleteTask(task.ID, DeleteTaskOptions{})

	plain, err := task.Store.AddIntent(map[string]any{"summary": "plain"}, 1, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	web, err := task.Store.AddIntent(map[string]any{"summary": "web", "role": "web"}, 9, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}

	e := NewEngine(m)
	got, token := e.claimNext(task, "web#1", "web")
	if got == nil || got.ID != web {
		t.Fatalf("web agent claimed %v, want intent %d", got, web)
	}
	if token == "" {
		t.Fatal("claim returned no token")
	}
	got, _ = e.claimNext(task, "general#0", "general")
	if got == nil || got.ID != plain {
		t.Fatalf("general agent did not claim the role-less intent: %v", got)
	}
}
