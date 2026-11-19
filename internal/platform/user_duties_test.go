package platform

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestUserDutiesAreLabelsPreservedBySyncAndSnapshot(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	u, err := r.SaveUser(User{Username: "alice", DisplayName: "Alice", Role: RoleMember, Enabled: true, Duties: []string{"frontend", "backend", "frontend"}}, "password-123")
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != RoleMember || len(u.Duties) != 2 {
		t.Fatal(u)
	}
	u, err = r.SaveUser(User{Username: "alice", DisplayName: "Renamed", Role: RoleMember, Enabled: true}, "")
	if err != nil || len(u.Duties) != 2 {
		t.Fatal("omitted labels cleared", err)
	}
	snapshot, err := r.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	other, err := OpenRegistry(filepath.Join(t.TempDir(), "other.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err = other.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	users, _ := other.Users()
	if len(users) != 1 || !reflect.DeepEqual(users[0].Duties, u.Duties) {
		t.Fatal(users)
	}
	if _, err = r.SaveUser(User{Username: "alice", Role: RoleMember, Enabled: true, Duties: []string{"superadmin"}}, ""); err == nil {
		t.Fatal("unknown duty accepted")
	}
}
