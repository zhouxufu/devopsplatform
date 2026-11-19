package deploy

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

func TestReleaseNumbersAreApplicationLocalAcrossEnvironmentsAndPersistent(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	create := func(service, env string, want int64) *ReleaseOrder {
		t.Helper()
		o := &ReleaseOrder{Service: service, Environment: env, Status: "failed", Actor: "qa"}
		if err := s.CreateReleaseOrder(o); err != nil {
			t.Fatal(err)
		}
		if o.Number != want {
			t.Fatalf("%s/%s number=%d want=%d", service, env, o.Number, want)
		}
		got := s.GetReleaseOrder(o.ID)
		if got == nil || got.Number != want || got.ID != o.ID || got.Service != service {
			t.Fatalf("global identity changed: %+v", got)
		}
		return o
	}
	a := create("orders", "test", 1)
	b := create("billing", "test", 1)
	if a.ID == b.ID {
		t.Fatal("global ID collision")
	}
	a2 := create("orders", "prod", 2)
	if s.ReleaseOrderNumber(a.ID, "billing") != 0 {
		t.Fatal("number leaked across application")
	}
	if _, err = s.db.Exec(`DELETE FROM release_orders WHERE id=?`, a2.ID); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	create("orders", "test", 3)
	if list := s.ReleaseOrders("orders", 20); len(list) != 2 || list[0].Number != 3 {
		t.Fatal(list)
	}
	page, err := s.BuildList("releases", BuildListFilter{Service: "orders"})
	if err != nil || len(page.Items) != 2 {
		t.Fatal(page, err)
	}
	if page.Items[0].(*ReleaseOrder).Number != 3 {
		t.Fatal("page lost local number")
	}
	if s.ReleaseOrderLabel(a.ID, "orders") != "#1" {
		t.Fatal("wrong display label")
	}
}

func TestReleaseNumberBackfillPreservesOldReferencesAndIsIdempotent(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, old := range []struct {
		id  int
		app string
	}{{4, "orders"}, {18, "billing"}, {19, "orders"}, {20, "orders"}} {
		if _, err = s.db.Exec(`INSERT INTO release_orders(id,service,artifact_id,version,environment,status,actor,started_at) VALUES(?,?,0,'v1','test','failed','qa',1)`, old.id, old.app); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.db.Exec(`INSERT INTO release_order_deploys(release_id,deploy_id,batch_no) VALUES(19,991,1)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.initReleaseOrderNumbers(); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[int64]int64{4: 1, 18: 1, 19: 2, 20: 3} {
		o := s.GetReleaseOrder(id)
		if o == nil || o.Number != want || o.ID != id {
			t.Fatal(o, want)
		}
	}
	if ids := s.ReleaseDeployIDs(19); len(ids) != 1 || ids[0] != 991 {
		t.Fatal("deploy reference changed", ids)
	}
	// APIs still resolve a global ID even though two applications both show #1.
	h := Routes(s, NewBuilder(s, t.TempDir(), NewLogHub()), NewLogHub(), "", NewStepLib(filepath.Join("..", "..", "steps")))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/releases/18", nil))
	var got ReleaseOrder
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.ID != 18 || got.Number != 1 || got.Service != "billing" {
		t.Fatal(w.Code, w.Body.String())
	}
	// Existing log evidence is not rewritten by display-number backfill.
	if s.ReleaseOrderLabel(999, "orders") != "记录 ID 999" {
		t.Fatal("unknown number guessed")
	}
}

func TestReleaseNumberAllocationRollsBackOnRejectedOrder(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bad := &ReleaseOrder{Service: "orders", ArtifactID: 999, Status: "failed"}
	if s.CreateReleaseOrder(bad) == nil {
		t.Fatal("missing artifact accepted")
	}
	o := &ReleaseOrder{Service: "orders", Status: "failed"}
	if err = s.CreateReleaseOrder(o); err != nil || o.Number != 1 {
		t.Fatal("rejected attempt consumed a number", o, err)
	}
}

func TestReleaseNumbersConcurrentAcrossStores(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 0 {
				s = b
			}
			o := &ReleaseOrder{Service: fmt.Sprintf("app-%d", i%2), Environment: fmt.Sprint(i % 3), Status: "failed"}
			if e := s.CreateReleaseOrder(o); e != nil {
				errs <- e
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	for _, app := range []string{"app-0", "app-1"} {
		orders := a.ReleaseOrders(app, 30)
		numbers := []int{}
		for _, o := range orders {
			numbers = append(numbers, int(o.Number))
		}
		sort.Ints(numbers)
		if len(numbers) != 8 {
			t.Fatal(app, numbers)
		}
		for i, n := range numbers {
			if n != i+1 {
				t.Fatal("duplicate or missing number", app, numbers)
			}
		}
	}
}

func TestReleaseNumbersDoNotBypassProtectedApprovalOrActiveReservation(t *testing.T) {
	s, m, req := deliveryFixture(t)
	o, err := m.CreateArtifact("orders", req.Artifacts["orders"], "prod", "synthetic protected release", "alice")
	if err != nil || o.Number != 1 || o.Status != "pending_approval" {
		t.Fatal(o, err)
	}
	if m.Approve(o.ID, "alice") == nil {
		t.Fatal("numbering bypassed self-approval guard")
	}
	if _, err = m.CreateArtifact("orders", o.ArtifactID, "prod", "duplicate", "alice"); err == nil {
		t.Fatal("numbering bypassed active reservation")
	}
	if s.GetReleaseOrder(o.ID).Status != "pending_approval" || len(s.Deploys("orders", 10)) != 0 {
		t.Fatal("number creation executed deployment")
	}
}
