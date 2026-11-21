package db

import "testing"

func TestCoverageEnabledWithoutTask(t *testing.T) {
	on, err := (&AssetStore{}).CoverageEnabled(0)
	if err != nil || !on {
		t.Fatalf("enabled=%v err=%v", on, err)
	}
}

func TestCoverageEnabledMissingRowStaysOn(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	defer d.Close()
	on, err := d.Assets().CoverageEnabled(9_000_000_000_001)
	if err != nil || !on {
		t.Fatalf("missing task: enabled=%v err=%v, want enabled with no error", on, err)
	}
}

func TestCoverageEnabledQueryErrorStaysOn(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	on, err := d.Assets().CoverageEnabled(1)
	if err == nil || !on {
		t.Fatalf("failed query: enabled=%v err=%v, want enabled and the error", on, err)
	}
}

// TestAddAgentScopeReturnsStoredID: the POST /api/tasks/{id}/scope response must
// carry the persisted row id, not 0, so a client can delete the row it just added.
// The upsert is idempotent, so the duplicate call must return the same id.
func TestAddAgentScopeReturnsStoredID(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	defer d.Close()
	task, err := d.CreateTask("scope id", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	as := d.Assets()
	added, err := as.AddAgentScope(task.ID, "ip", "127.0.0.1", "manual smoke", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if added.ID == 0 {
		t.Fatal("AddAgentScope returned id 0; the stored row id must be filled")
	}
	list, err := as.ListTaskScope(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != added.ID {
		t.Fatalf("stored scope %+v does not match returned id %d", list, added.ID)
	}
	dup, err := as.AddAgentScope(task.ID, "ip", "127.0.0.1", "manual smoke", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if dup.ID != added.ID {
		t.Fatalf("duplicate add returned id %d, want the existing %d", dup.ID, added.ID)
	}
}

// TestStripHostPort covers the ip:port / [ipv6]:port stripping used by the
// ip/cidr scope path so a target like "10.0.188.136:3000" no longer 404s.
func TestStripHostPort(t *testing.T) {
	cases := []struct{ in, want string }{
		{"10.0.188.136:3000", "10.0.188.136"},       // the reported IP case
		{"10.0.188.136", "10.0.188.136"},            // bare IPv4 unchanged
		{"[2001:db8::1]:8080", "2001:db8::1"},       // bracketed IPv6 + port
		{"2001:db8::1", "2001:db8::1"},              // bare IPv6 unchanged (has colons)
		{" 1.2.3.4:80 ", "1.2.3.4"},                 // trims surrounding space
		{"example.com:443", "example.com"},          // domain + port → bare host
		{"api.example.com:8080", "api.example.com"}, // subdomain + port
		{"example.com", "example.com"},              // bare domain unchanged
	}
	for _, c := range cases {
		if got := stripHostPort(c.in); got != c.want {
			t.Errorf("stripHostPort(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
