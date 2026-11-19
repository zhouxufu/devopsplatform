package deploy

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestPipelineNumbersAreApplicationLocalPersistentAndNotRouteIDs(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	create := func(service string, want int64) *PipelineRun {
		t.Helper()
		r := &PipelineRun{Service: service, StartedAt: time.Now().Unix()}
		if err := st.CreateRun(r); err != nil {
			t.Fatal(err)
		}
		if r.Number != want {
			t.Fatalf("%s number=%d want=%d", service, r.Number, want)
		}
		if got := st.GetRun(r.ID); got == nil || got.Number != want || got.Service != service {
			t.Fatalf("route ID lookup changed: %+v", got)
		}
		return r
	}
	a := create("orders", 1)
	b := create("billing", 1)
	result, e := st.db.Exec(`INSERT INTO artifacts(service,version,kind,uri,digest,metadata,source,source_id,created_at) VALUES('orders','v1','jar','fixture.jar','sha256:fixture','{"number":"999"}','pipeline-run',?,1)`, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	artifactID, _ := result.LastInsertId()
	if artifact := st.GetArtifact(artifactID); artifact == nil || artifact.SourceNumber != 1 || artifact.SourceID != a.ID {
		t.Fatal("artifact source identity/number changed", artifact)
	}
	if artifacts := st.Artifacts("orders", 10); len(artifacts) != 1 || artifacts[0].SourceNumber != 1 {
		t.Fatal("artifact list number missing", artifacts)
	}
	if a.ID == b.ID {
		t.Fatal("global identity collision")
	}
	if r := st.ActiveRun("orders"); r == nil || r.Number != 1 {
		t.Fatal("active run lost sequence")
	}
	if err := st.CreateRun(&PipelineRun{Service: "orders"}); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("lost active protection: %v", err)
	}
	st.FinishRun(a.ID, "failed")
	a2 := create("orders", 2)
	st.FinishRun(a2.ID, "success")
	// Deleting history must not reuse a previously issued display number.
	if _, err := st.db.Exec(`DELETE FROM pipeline_runs WHERE id=?`, a2.ID); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a3 := create("orders", 3)
	if st.PipelineRunNumber(a3.ID, "billing") != 0 {
		t.Fatal("cross-application number lookup")
	}
	if got := st.Runs("orders", 1); len(got) != 1 || got[0].Number != 3 {
		t.Fatal(got)
	}
	if page, e := st.BuildList("runs", BuildListFilter{Service: "orders"}); e != nil || len(page.Items) == 0 {
		t.Fatal(e, page)
	}
}

func TestPipelineNumberBackfillIsOrderedAndIdempotent(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, r := range []struct {
		id      int
		service string
	}{{4, "orders"}, {17, "billing"}, {18, "orders"}} {
		if _, e := st.db.Exec(`INSERT INTO pipeline_runs(id,service,status,actor,started_at) VALUES(?,?,'failed','qa',1)`, r.id, r.service); e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 2; i++ {
		if err = st.initPipelineRunNumbers(); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[int64]int64{4: 1, 17: 1, 18: 2} {
		r := st.GetRun(id)
		if r == nil || r.ID != id || r.Number != want {
			t.Fatalf("backfill %+v want %d", r, want)
		}
	}
	r := &PipelineRun{Service: "orders"}
	if err = st.CreateRun(r); err != nil || r.Number != 3 {
		t.Fatal(err, r)
	}
}

func TestPipelineNumbersConcurrentAcrossStoreConnections(t *testing.T) {
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
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st := a
			if i%2 == 0 {
				st = b
			}
			r := &PipelineRun{Service: fmt.Sprintf("app-%d", i%3)}
			e := st.CreateRun(r)
			if e != nil {
				if !errors.Is(e, ErrRunConflict) {
					errs <- e
				}
				return
			}
			st.FinishRun(r.ID, "failed")
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	for i := 0; i < 3; i++ {
		service := fmt.Sprintf("app-%d", i)
		runs := a.Runs(service, 20)
		for j, r := range runs {
			if r.Number != int64(len(runs)-j) {
				t.Fatalf("sequence gap/duplicate: %+v", runs)
			}
		}
	}
}
