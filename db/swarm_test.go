package db

import "testing"

func TestAgentRoleMix(t *testing.T) {
	if got, want := AgentRoleMix(3, false), []string{AgentRoleGeneral, AgentRoleRecon, AgentRoleWeb}; !equalStrings(got, want) {
		t.Fatalf("mix(3)=%v, want %v", got, want)
	}
	got := AgentRoleMix(7, true)
	if len(got) != 8 || got[7] != AgentRoleChain {
		t.Fatalf("chain role must be last and unique: %v", got)
	}
	if got[6] != mixRoles[0] {
		t.Fatalf("mix must cycle: %v", got)
	}
	if len(AgentRoleMix(0, false)) != 1 {
		t.Fatal("zero size must still yield one agent")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A role agent claims the role-matching intent, a general agent claims both, and a
// claim never hands out an agent twice.
func TestClaimAgent(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	defer d.Close()

	tk, err := d.CreateTask("claim agents", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(tk.ID)

	roster, err := d.EnsureTaskAgents(tk.ID, AgentRoleMix(3, false))
	if err != nil {
		t.Fatal(err)
	}
	if len(roster) != 3 || roster[0].Role != AgentRoleGeneral || roster[1].Name != "recon#1" {
		t.Fatalf("unexpected roster %+v", roster)
	}

	web, err := d.ClaimAgent(tk.ID, AgentRoleWeb)
	if err != nil || web == nil {
		t.Fatalf("claim web: %v %v", web, err)
	}
	if web.Role != AgentRoleWeb {
		t.Fatalf("claimed %s, want a web agent", web.Role)
	}
	// Only the general agent is idle now and it must still be claimable.
	gen, err := d.ClaimAgent(tk.ID, AgentRoleWeb)
	if err != nil || gen == nil {
		t.Fatalf("fallback claim: %v %v", gen, err)
	}
	if gen.Role != AgentRoleGeneral {
		t.Fatalf("fallback claimed %s", gen.Role)
	}
	if third, err := d.ClaimAgent(tk.ID, AgentRoleAPI); err != nil || third == nil {
		t.Fatalf("third claim %v %v, want the recon agent", third, err)
	}
	if fourth, err := d.ClaimAgent(tk.ID, AgentRoleAPI); err != nil || fourth != nil {
		t.Fatalf("fourth claim %v %v, want nil", fourth, err)
	}

	if err := d.FinishAgent(web.ID, true); err != nil {
		t.Fatal(err)
	}
	var done, findings int
	if err := d.QueryRow(`SELECT intents_done, findings FROM task_agents WHERE id=$1`, web.ID).Scan(&done, &findings); err != nil {
		t.Fatal(err)
	}
	if done != 1 || findings != 1 {
		t.Fatalf("counters not bumped: done=%d findings=%d", done, findings)
	}

	// Shrinking the roster retires the extra slot.
	shrunk, err := d.EnsureTaskAgents(tk.ID, []string{AgentRoleGeneral})
	if err != nil {
		t.Fatal(err)
	}
	if len(shrunk) != 1 {
		t.Fatalf("want 1 live agent, got %d", len(shrunk))
	}
}

// FrontierForRole orders role-matching intents first.
func TestFrontierForRole(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	defer d.Close()

	tk, err := d.CreateTask("role frontier", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(tk.ID)
	es := d.Exploration(tk.ExplorationID)
	plain, err := es.AddIntent(map[string]any{"summary": "plain"}, 1, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	web, err := es.AddIntent(map[string]any{"summary": "web", "role": "web"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	work, err := es.AddIntent(map[string]any{"summary": "web2", "role": "web"}, 2, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	got, err := es.FrontierForRole("web", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 intents, got %d", len(got))
	}
	if got[0].ID != web || got[1].ID != work {
		t.Fatalf("role-matching intents must be first: %d %d (web=%d work=%d)", got[0].ID, got[1].ID, web, work)
	}
	if got[2].ID != plain {
		t.Fatalf("role-less intent must be last, got %d", got[2].ID)
	}
}
