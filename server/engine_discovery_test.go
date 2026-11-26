package server

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
)

// A queued peer discovery is injected once, ahead of steering, and survives a
// restart through the persisted event.
func TestDiscoveryDrain(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer m.Close()

	task, err := m.CreateTaskWithOptions("discovery drain", "drain", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.DeleteTask(task.ID, DeleteTaskOptions{})
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)

	item := agent.DiscoveryItem{Kind: "fact", Summary: "login form at /admin", NodeID: 7, Agent: "web#2"}
	task.QueueDiscovery(item)
	if _, err := m.pg.AddDiscovery(db.DiscoveryEvent{
		TaskID: taskID, AgentName: item.Agent, Kind: item.Kind, Summary: item.Summary, NodeID: item.NodeID,
	}); err != nil {
		t.Fatal(err)
	}

	h := steerHooks{
		discoveries: func() string { return agent.RenderDiscoveries(task.DrainDiscoveries()) },
		drain:       func() (string, bool) { return "", false },
	}
	blocked, msg, _ := h.PreToolUse(context.Background(), "list_assets", nil)
	if !blocked || !strings.Contains(msg, "login form at /admin") || !strings.Contains(msg, "web#2") {
		t.Fatalf("discovery not injected: blocked=%v msg=%q", blocked, msg)
	}
	if blocked, _, _ := h.PreToolUse(context.Background(), "list_assets", nil); blocked {
		t.Fatal("discovery must be drained after one injection")
	}

	// A restarted run still sees it.
	recent, err := m.pg.RecentDiscoveries(taskID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].Summary != item.Summary {
		t.Fatalf("persisted discovery lost: %+v", recent)
	}
}
