package monitor

import "testing"

func TestMonitorMigrationCatalogMatchesSQLiteSchema(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := NewTraceStore(st.DB(), true); err != nil {
		t.Fatal(err)
	}
	if NewOtlpReceiver(st.DB()) == nil {
		t.Fatal("OTLP schema initialization failed")
	}
	_ = ensureAvailRulesTable(st.DB())
	_ = NewMetricAlertEngine(st.DB())
	if err := machineTable(st.DB()); err != nil {
		t.Fatal(err)
	}
	for _, table := range monitorMigrationTables {
		columns, err := sqliteMonitorTableColumns(st.DB().DB, table.name)
		if err != nil {
			t.Fatalf("%s: %v", table.name, err)
		}
		if len(columns) == 0 {
			t.Fatalf("migration table %s does not exist", table.name)
		}
		for key := range table.keys {
			if !columns[key] {
				t.Errorf("migration table %s is missing key column %s", table.name, key)
			}
		}
	}
}
