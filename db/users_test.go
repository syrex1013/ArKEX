package db

import (
	"testing"
)

func TestAuthUsersCRUD(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	u, err := d.CreateAuthUser("alice", "hash-alice", "operator")
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "alice" || u.Role != "operator" || u.PwdVersion != 1 {
		t.Fatalf("unexpected user %+v", u)
	}
	if _, err := d.CreateAuthUser("alice", "x", "operator"); err != ErrUserExists {
		t.Fatalf("want ErrUserExists, got %v", err)
	}
	if _, err := d.CreateAuthUser("ARTEX", "x", "admin"); err == nil {
		t.Fatal("expected reserved username error")
	}
	got, err := d.GetAuthUserByName("alice")
	if err != nil || got.ID != u.ID {
		t.Fatalf("lookup: %+v %v", got, err)
	}
	ver, err := d.RotateAuthUserPassword(u.ID, "hash-2")
	if err != nil || ver != 2 {
		t.Fatalf("rotate: ver=%d err=%v", ver, err)
	}
	if err := d.SetAuthUserDisabled(u.ID, true); err != nil {
		t.Fatal(err)
	}
	got, err = d.GetAuthUserByID(u.ID)
	if err != nil || !got.Disabled {
		t.Fatalf("disabled: %+v %v", got, err)
	}
}
