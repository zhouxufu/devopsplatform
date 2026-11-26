package deploy

import "testing"

func TestCodeDraftWithoutBranchMustBeCompletedBeforeReady(t *testing.T) {
	store, _, _ := deliveryFixture(t)
	draft := WorkDeliverySave{Changes: []Change{{Kind: "code", Service: "orders", Title: "订单修订"}}}
	if err := store.CreateChangeBatch(&draft, "alice"); err != nil {
		t.Fatal(err)
	}
	id := draft.Changes[0].ID
	page, err := store.DeliveryRecordsPage(DeliveryRecordFilter{Type: "changes", Query: "订单修订"})
	if err != nil || len(page.Items) != 1 || !page.Items[0].MissingBranch {
		t.Fatalf("missing draft indicator: %+v %v", page, err)
	}
	if _, err := store.SetChangeReady(id, true); err == nil || store.GetChange(id).Status != ChangeDraft {
		t.Fatal("incomplete draft became ready")
	}
	batch := WorkDeliverySave{MarkReady: true, Changes: []Change{
		{Kind: "code", Service: "orders", Title: "第一项", Reference: "feature/orders"},
		{Kind: "code", Service: "orders", Title: "第二项"},
	}}
	if err := store.CreateChangeBatch(&batch, "alice"); err == nil || batch.Changes[0].ID != 0 {
		t.Fatal("incomplete batch accepted")
	}
}

func TestDeliveryEntryReadyUsesSharedAndCatalogResources(t *testing.T) {
	s, _, g := resourceGroupFixture(t)
	if err := s.UpsertConnection(ResourceConnection{Name: "config", Kind: "nacos", Endpoint: "http://127.0.0.1:1", Config: map[string]string{"nacos_auth": "none"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveApplicationResources("orders", ApplicationResources{CompositionVersion: 2, ResourceGroups: []string{g.Name}, GroupRevisions: map[string]int64{g.Name: g.Revision}, NacosConfigs: []ApplicationNacosConfig{{Alias: "settings", Environment: "test", Connection: "config", Group: "TEST", DataID: "orders-test.yaml"}}}); err != nil {
		t.Fatal(err)
	}
	w := &WorkItem{Title: "配置与菜单变更"}
	if err := s.CreateWorkItem(w); err != nil {
		t.Fatal(err)
	}
	base, _ := s.WorkDelivery(w.ID)
	req := WorkDeliverySave{Digest: base.Digest, MarkReady: true, Changes: []Change{
		{Kind: "mysql", Title: "菜单", Service: "orders", SQL: &SQLChange{ResourceAlias: "db", Content: "UPDATE menu SET enabled=1 WHERE id=1", MaxAffectedRows: 1}},
		{Kind: "nacos", Title: "开关", Service: "orders", Nacos: &NacosChange{ResourceAlias: "settings", Group: "DEFAULT_GROUP", Format: "yaml", Entries: []NacosEntry{{Key: "feature.enabled", Action: "set", Value: "true"}}}},
	}}
	if err := s.SaveWorkDelivery(w.ID, &req, "alice", false); err != nil {
		t.Fatal(err)
	}
	after, _ := s.WorkDelivery(w.ID)
	if len(after.Changes) != 2 {
		t.Fatal(after)
	}
	for _, c := range after.Changes {
		if c.Status != ChangeReady || c.Owner != "alice" {
			t.Fatal(c)
		}
	}
	var executions int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM sql_execution_ledger)+(SELECT COUNT(*) FROM nacos_execution_ledger)`).Scan(&executions); err != nil || executions != 0 {
		t.Fatal("readiness executed resources", err, executions)
	}
}

func TestDeliveryEntryReadyIsAtomicAndOwnerScoped(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	w := &WorkItem{Title: "多人维护"}
	s.CreateWorkItem(w)
	for _, owner := range []string{"alice", "bob"} {
		base, _ := s.WorkDelivery(w.ID)
		if err := s.SaveWorkDelivery(w.ID, &WorkDeliverySave{Digest: base.Digest, Changes: []Change{{Kind: "code", Title: owner, Service: "orders", Reference: "feature/" + owner}}}, owner, false); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := s.WorkDelivery(w.ID)
	if err := s.SaveWorkDelivery(w.ID, &WorkDeliverySave{Digest: before.Digest, MarkReady: true, Changes: append([]Change{}, before.Changes...)}, "alice", false); err == nil {
		t.Fatal("marked another owner's row ready")
	}
	after, _ := s.WorkDelivery(w.ID)
	if after.Digest != before.Digest {
		t.Fatal("partial status change")
	}
	if err := s.SaveWorkDelivery(w.ID, &WorkDeliverySave{Digest: "stale", MarkReady: true, Changes: []Change{before.Changes[0]}}, "alice", false); err == nil {
		t.Fatal("stale ready accepted")
	}
	if err := s.SaveWorkDelivery(w.ID, &WorkDeliverySave{Digest: before.Digest, MarkReady: true, Changes: []Change{before.Changes[0]}}, "alice", false); err != nil {
		t.Fatal(err)
	}
	after, _ = s.WorkDelivery(w.ID)
	if after.Changes[0].Status != ChangeReady || after.Changes[1].Status != ChangeDraft {
		t.Fatal("expanded readiness scope")
	}
}

func TestDeliveryEntryMissingSharedAliasRollsBackDraftAndReady(t *testing.T) {
	s, _, _ := resourceGroupFixture(t)
	w := &WorkItem{Title: "资源未声明"}
	s.CreateWorkItem(w)
	base, _ := s.WorkDelivery(w.ID)
	for _, ready := range []bool{false, true} {
		req := WorkDeliverySave{Digest: base.Digest, MarkReady: ready, Changes: []Change{{Kind: "manual", Title: "不能残留", Reference: "人工核对"}, {Kind: "mysql", Title: "无此资源", Service: "orders", SQL: &SQLChange{ResourceAlias: "not-declared", Content: "UPDATE menu SET enabled=1 WHERE id=1", MaxAffectedRows: 1}}}}
		if err := s.SaveWorkDelivery(w.ID, &req, "alice", false); err == nil {
			t.Fatal("unknown alias accepted")
		}
		after, _ := s.WorkDelivery(w.ID)
		if after.Digest != base.Digest {
			t.Fatal("partial save")
		}
	}
}

func TestDeliveryEntryIndependentReadyAndSharedOriginGuard(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	req := WorkDeliverySave{MarkReady: true, Changes: []Change{{Kind: "manual", Title: "独立记录", Reference: "核对菜单"}}}
	if err := s.CreateChangeBatch(&req, "alice"); err != nil {
		t.Fatal(err)
	}
	if s.GetChange(req.Changes[0].ID).Status != ChangeReady {
		t.Fatal("independent readiness lost")
	}
	a, b := &WorkItem{Title: "来源"}, &WorkItem{Title: "引用"}
	s.CreateWorkItem(a)
	s.CreateWorkItem(b)
	base, _ := s.WorkDelivery(a.ID)
	r := WorkDeliverySave{Digest: base.Digest, AlsoWorkItemIDs: []int64{b.ID}, Changes: []Change{{Kind: "code", Title: "共享实现", Service: "orders", Reference: "feature/shared", DeliveryOwnership: &ChangeDeliveryOwnership{Mode: "self"}}}}
	if err := s.SaveWorkDelivery(a.ID, &r, "alice", false); err != nil {
		t.Fatal(err)
	}
	linked, _ := s.WorkDelivery(b.ID)
	if err := s.SaveWorkDelivery(b.ID, &WorkDeliverySave{Digest: linked.Digest, MarkReady: true, Changes: linked.Changes}, "admin", true); err == nil {
		t.Fatal("other origin changed via administrator editor")
	}
}
