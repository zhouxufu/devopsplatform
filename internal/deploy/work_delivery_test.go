package deploy

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkDeliveryAtomicConflictAndOwnership(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w := &WorkItem{Title: "需求"}
	other := &WorkItem{Title: "共享 BUG"}
	for _, v := range []*WorkItem{w, other} {
		if err = st.CreateWorkItem(v); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"api", "web"} {
		if err = st.UpsertService(Service{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	base, _ := st.WorkDelivery(w.ID)
	req := &WorkDeliverySave{Digest: base.Digest, AlsoWorkItemIDs: []int64{other.ID}, Changes: []Change{{Kind: "code", Title: "分支", Service: "api", Reference: "feature/menu"}, {Kind: "code", Title: "分支", Service: "web", Reference: "feature/menu"}, {Kind: "manual", Title: "核验"}}}
	if err = st.SaveWorkDelivery(w.ID, req, "alice", false); err != nil {
		t.Fatal(err)
	}
	got, _ := st.WorkDelivery(w.ID)
	if len(got.Changes) != 3 {
		t.Fatal("multi-app contents lost")
	}
	for _, c := range got.Changes {
		if c.Owner != "alice" || len(c.WorkItems) != 2 {
			t.Fatal("owner or shared association lost")
		}
	}
	bad := WorkDeliverySave{Digest: got.Digest, Changes: []Change{{Kind: "manual", Title: "不得留下"}, {Kind: "code", Title: "不存在的应用", Service: "missing", Reference: "main"}}}
	if err = st.SaveWorkDelivery(w.ID, &bad, "alice", false); err == nil {
		t.Fatal("invalid app accepted")
	}
	if after, _ := st.WorkDelivery(w.ID); after.Digest != got.Digest {
		t.Fatal("partial batch saved")
	}
	edit := got.Changes[0]
	edit.Reference = "feature/changed"
	if err = st.SaveWorkDelivery(w.ID, &WorkDeliverySave{Digest: got.Digest, Changes: []Change{edit}}, "bob", false); err == nil {
		t.Fatal("ownership bypass")
	}
	if err = st.SaveWorkDelivery(w.ID, &WorkDeliverySave{Digest: got.Digest, Changes: []Change{edit}}, "alice", false); err != nil {
		t.Fatal(err)
	}
	if err = st.SaveWorkDelivery(w.ID, &WorkDeliverySave{Digest: got.Digest, Changes: []Change{{Kind: "manual", Title: "stale"}}}, "alice", false); err == nil {
		t.Fatal("stale edit accepted")
	}
	got, _ = st.WorkDelivery(w.ID)
	if len(got.Changes) != 3 || len(got.Changes[0].WorkItems) != 2 {
		t.Fatal("shared edit detached other BUG")
	}
	if _, err = st.SetChangeReady(edit.ID, true); err != nil {
		t.Fatal(err)
	}
	got, _ = st.WorkDelivery(w.ID)
	if err = st.SaveWorkDelivery(w.ID, &WorkDeliverySave{Digest: got.Digest, Changes: []Change{edit}}, "alice", true); err == nil {
		t.Fatal("ready content edited")
	}
}
func TestWorkDeliveryNacosAndAPI(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w := &WorkItem{Title: "批量"}
	st.CreateWorkItem(w)
	st.UpsertService(Service{Name: "api"})
	if err = st.SaveApplicationResources("api", ApplicationResources{Requirements: []ApplicationResourceRequirement{{Alias: "config", Kind: "nacos"}}}); err != nil {
		t.Fatal(err)
	}
	base, _ := st.WorkDelivery(w.ID)
	req := WorkDeliverySave{Digest: base.Digest, Changes: []Change{{Kind: "nacos", Title: "配置", Service: "api", Nacos: &NacosChange{ResourceAlias: "config", DataID: "api.yaml", Group: "DEFAULT_GROUP", Format: "yaml", Entries: []NacosEntry{{Key: "timeout", Value: "", Action: "set"}, {Key: "old", Value: "discard", Action: "delete"}}}}}}
	raw, _ := json.Marshal(req)
	h := Routes(st, NewBuilder(st, dir, NewLogHub()), NewLogHub(), "", NewStepLib(dir+"/steps", dir+"/custom"))
	r := httptest.NewRequest("PUT", fmt.Sprintf("/api/work-items/%d/delivery-content", w.ID), strings.NewReader(string(raw)))
	r.Header.Set("X-User", "alice")
	out := httptest.NewRecorder()
	h.ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	got, _ := st.WorkDelivery(w.ID)
	if len(got.Changes) != 1 || got.Changes[0].Nacos.Entries[1].Value != "" {
		t.Fatal("structured config not round-tripped")
	}
	req.Digest = got.Digest
	req.Changes[0].Nacos.Entries = append(req.Changes[0].Nacos.Entries, NacosEntry{Key: "timeout", Action: "set"})
	if err = st.SaveWorkDelivery(w.ID, &req, "alice", false); err == nil {
		t.Fatal("duplicate config key accepted")
	}
}
