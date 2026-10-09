package deploy

import "testing"

func TestDeploymentMigrationCatalogMatchesSQLiteSchema(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, table := range deployMigrationTables {
		columns, err := sqliteTableColumns(st.db.DB, table.name)
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
		if table.name == "work_items" || table.name == "work_item_attachments" {
			for _, column := range table.columns {
				if !columns[column] {
					t.Errorf("migration table %s is missing content column %s", table.name, column)
				}
			}
		}
	}
}

func TestMigrationIdentifierMapping(t *testing.T) {
	if got := quotedColumns([]string{"key", "trigger"}, "`"); got != "`key`,`trigger`" {
		t.Fatalf("quoted columns = %q", got)
	}
	var meta, pipeline *deployMigrationTable
	for i := range deployMigrationTables {
		switch deployMigrationTables[i].name {
		case "deploy_meta":
			meta = &deployMigrationTables[i]
		case "pipeline_runs":
			pipeline = &deployMigrationTables[i]
		}
	}
	if meta == nil || meta.rename["key"] != "meta_key" {
		t.Fatal("deploy_meta key mapping is missing")
	}
	if pipeline == nil || pipeline.rename["trigger"] != "run_trigger" {
		t.Fatal("pipeline trigger mapping is missing")
	}
}
