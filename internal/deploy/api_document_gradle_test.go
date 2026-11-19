package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"logplatform/pkg/apidoc"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Uses a local wrapper fixture through the real build runner and snapshot store.
func TestGradleDocumentAutomaticEnvironmentBuildsAndHistory(t *testing.T) {
	st, h := apiTestingFixture(t)
	documentBrokerFixture(t)
	pr, sv := documentPipelineFixture(t, st)
	if err := st.UpsertEnvironment(Environment{Name: "dev", DisplayName: "Development", Purpose: "development"}); err != nil {
		t.Fatal(err)
	}
	v := *sv
	v.BuildTool = "gradle"
	v.GradleExecutable = "auto"
	v.GradleTasks = "clean build"
	v.GradleProjectProperties = "stage=test\nsmartdoc.configFile=stale.json"
	v.GradleSystemProperties = "fixture.system=retained"
	if err := st.UpsertService(v); err != nil {
		t.Fatal(err)
	}
	sv = st.GetService(v.Name)
	script := `#!/bin/sh
set -eu
config=''
stage=0
system=0
task=0
for arg in "$@"; do
 case "$arg" in
  -Dorg.gradle.project.smartdoc.configFile=*) config="${arg#*=}";;
  -Pstage=test) stage=1;;
  -Dfixture.system=retained) system=1;;
  smartDocOpenApi) task=1;;
  -Psmartdoc.configFile=*|clean|build) exit 91;;
 esac
done
test "$stage$system$task" = 111
test -f "$config"
mkdir -p build/api-docs
cp fixture-openapi.json build/api-docs/openapi.json
`
	if err := os.WriteFile(filepath.Join(sv.Workdir, "gradlew"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, env := range []string{"dev", "test"} {
		config := "smart-doc-" + env + ".json"
		os.WriteFile(filepath.Join(sv.Workdir, config), []byte(`{}`), 0600)
		settings, _ := json.Marshal(map[string]string{"config_file": config})
		if err := st.saveEnvironmentDocumentSource(sv, env, apidoc.Source{Provider: "smart-doc", OnBuild: true, Required: true, Settings: settings}, "fixture"); err != nil {
			t.Fatal(err)
		}
	}
	execute := func(env, raw string) *PipelineRun {
		t.Helper()
		os.WriteFile(filepath.Join(sv.Workdir, "fixture-openapi.json"), []byte(raw), 0600)
		frozen := *sv
		if err := st.freezeAPIDocumentation(&frozen, false, env); err != nil {
			t.Fatal(err)
		}
		if frozen.APIDocumentation == nil || frozen.APIDocumentation.Plan.Tool != "gradle" || frozen.APIDocumentation.Plan.InputFiles[0] != "smart-doc-"+env+".json" {
			t.Fatal(frozen.APIDocumentation)
		}
		run := &PipelineRun{Service: sv.Name, Actor: "fixture", StartedAt: time.Now().Unix(), Stages: pr.plannedStages(&frozen)}
		if err := st.CreateRun(run); err != nil {
			t.Fatal(err)
		}
		if err := st.saveBuildSnapshot(run.ID, &frozen, st.GetServiceDelivery(sv.Name)); err != nil {
			t.Fatal(err)
		}
		pr.runContext(context.Background(), run.ID, &frozen, false)
		out := st.GetRun(run.ID)
		if out.Status != "success" || len(out.Stages) != 2 || out.Stages[1].Status != "success" {
			t.Fatalf("%+v\n%s", out, st.RunLog(run.ID))
		}
		return out
	}
	first := execute("test", apiDocumentFixture)
	execute("dev", strings.Replace(apiDocumentFixture, "ok", "development", 1))
	second := execute("test", strings.Replace(apiDocumentFixture, "ok", "updated", 1))
	rows, err := st.db.Query(`SELECT id,environment,baseline_id,run_id FROM api_document_versions WHERE application_id=? ORDER BY id`, sv.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var firstID int64
	count := 0
	for rows.Next() {
		var id, base, run int64
		var env string
		rows.Scan(&id, &env, &base, &run)
		count++
		if count == 1 {
			firstID = id
			if run != first.ID || base != 0 || env != "test" {
				t.Fatal(id, base, env)
			}
		}
		if count == 2 && (env != "dev" || base != 0) {
			t.Fatal("cross-environment baseline", env, base)
		}
		if count == 3 && (env != "test" || base != firstID || run != second.ID) {
			t.Fatal(id, base, env)
		}
	}
	if count != 3 {
		t.Fatal(count)
	}
	if !strings.Contains(st.RunLog(second.ID), "修改 1") {
		t.Fatal(st.RunLog(second.ID))
	}
	production := *sv
	if err = st.freezeAPIDocumentation(&production, false, "prod"); err != nil || production.APIDocumentation != nil {
		t.Fatal("production auto-enabled", err)
	}
	response := apiTestingRequest(h, "GET", "/api/api-documents/source?service=orders&environment=test", "fixture", "admin", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"build_tool":"gradle"`) {
		t.Fatal(response.Body.String())
	}
	// Environment history remains readable using the same existing comparison API.
	response = apiTestingRequest(h, "GET", fmt.Sprintf("/api/api-documents?service=orders&environment=test"), "fixture", "member", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"modified":1`) {
		t.Fatal(response.Body.String())
	}
}
