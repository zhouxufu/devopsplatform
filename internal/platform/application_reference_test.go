package platform

import (
	"context"
	"encoding/json"
	"errors"
	"logplatform/internal/appcatalog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveVerifiesDeploymentReferencesAndPreservesHistory(t *testing.T) {
	for _, tc := range []struct {
		name    string
		refs    []appcatalog.ApplicationReference
		offline bool
		want    error
	}{
		{name: "stale", refs: []appcatalog.ApplicationReference{}},
		{name: "renamed-live-id", refs: []appcatalog.ApplicationReference{{ApplicationID: "fixed", Name: "new-name"}}, want: errApplicationHasDeployment},
		{name: "legacy-name", refs: []appcatalog.ApplicationReference{{Name: "old-name"}}, want: errApplicationHasDeployment},
		{name: "legacy-alias", refs: []appcatalog.ApplicationReference{{Name: "other-alias"}}, want: errApplicationHasDeployment},
		{name: "offline", offline: true, want: errDeploymentCheckUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			a, err := r.Observe("old-name", "deploy", "fixed")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = r.Observe("other-alias", "deploy", a.ApplicationID); err != nil {
				t.Fatal(err)
			}
			if err = r.RecordDeployment(DeploymentState{Service: a.Name, Environment: "test", Version: "history", Digest: "sha256:original"}); err != nil {
				t.Fatal(err)
			}
			if err = r.setApplicationDisplayName(a.ApplicationID, "历史名称", a.Name, "admin"); err != nil {
				t.Fatal(err)
			}
			var idsBefore int
			r.db.QueryRow(`SELECT COUNT(*) FROM application_identities`).Scan(&idsBefore)
			r.deploymentReferences = func(context.Context) ([]appcatalog.ApplicationReference, error) {
				if tc.offline {
					return nil, errors.New("offline")
				}
				return tc.refs, nil
			}
			if err = r.archiveApplication(a.ApplicationID, "admin"); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			all, err := r.List()
			if err != nil || len(all) != 1 {
				t.Fatal(all, err)
			}
			var audit, idsAfter int
			r.db.QueryRow(`SELECT COUNT(*) FROM application_lifecycle_events`).Scan(&audit)
			r.db.QueryRow(`SELECT COUNT(*) FROM application_identities`).Scan(&idsAfter)
			if idsBefore != idsAfter || all[0].ApplicationID != a.ApplicationID || all[0].DisplayName != "历史名称" {
				t.Fatal("identity or label changed", all)
			}
			history := r.DeploymentStates()
			if len(history) != 1 || history[0].Digest != "sha256:original" {
				t.Fatal("history changed", history)
			}
			if tc.want == nil {
				if all[0].Status != "archived" || containsString(all[0].Sources, "deploy") || audit != 2 {
					t.Fatal(all, audit)
				}
				if err = r.archiveApplication(a.ApplicationID, "admin"); err != nil {
					t.Fatal(err)
				}
				var count int
				r.db.QueryRow(`SELECT COUNT(*) FROM application_lifecycle_events`).Scan(&count)
				if count != audit {
					t.Fatal("retry duplicated audit")
				}
			} else if all[0].Status != "managed" || !containsString(all[0].Sources, "deploy") || audit != 0 {
				t.Fatal("failed check changed catalog", all, audit)
			}
		})
	}
}

func TestArchiveChecksLiveDeploymentEvenWhenSourceMarkerMissing(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	a, _ := r.Observe("manual", "manual", "")
	r.deploymentReferences = func(context.Context) ([]appcatalog.ApplicationReference, error) {
		return []appcatalog.ApplicationReference{{Name: "canonical", ApplicationID: a.ApplicationID}}, nil
	}
	if err = r.archiveApplication(a.ApplicationID, "admin"); !errors.Is(err, errApplicationHasDeployment) {
		t.Fatal(err)
	}
}

func TestDeploymentReferenceReaderFailsClosed(t *testing.T) {
	for _, body := range []string{`[]`, `{}`, `{"complete":true}`, `{"complete":false,"applications":[]}`, `{"complete":true,"applications":[{}]}`, `{"complete":true,"applications":[]} {}`, `{"complete":true,"applications":[]}`, `{"complete":true,"applications":[{"name":"orders","application_id":"fixed"}]}`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/application-references" || r.Method != "GET" {
					t.Error(r.URL.Path)
				}
				w.Write([]byte(body))
			}))
			defer srv.Close()
			g := NewGateway("")
			g.SetPlugins([]*Manifest{{Name: "delivery-core", AdminAddr: strings.TrimPrefix(srv.URL, "http://")}})
			refs, err := g.applicationDeploymentReferences(context.Background())
			valid := body == `{"complete":true,"applications":[]}` || strings.Contains(body, `"name":"orders"`)
			if valid != (err == nil) {
				t.Fatal(refs, err)
			}
		})
	}
	g := NewGateway("")
	if _, err := g.applicationDeploymentReferences(context.Background()); err == nil {
		t.Fatal("missing backend accepted")
	}
}

func TestArchiveStaleDetachRollsBackTogetherWithAudit(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	a, _ := r.Observe("stale", "deploy", "")
	r.deploymentReferences = func(context.Context) ([]appcatalog.ApplicationReference, error) {
		return []appcatalog.ApplicationReference{}, nil
	}
	r.db.Exec(`DROP TABLE application_lifecycle_events`)
	if err = r.archiveApplication(a.ApplicationID, "admin"); err == nil {
		t.Fatal("audit failure ignored")
	}
	all, _ := r.List()
	raw, _ := json.Marshal(all)
	if len(all) != 1 || !containsString(all[0].Sources, "deploy") || all[0].Status != "managed" {
		t.Fatal(string(raw))
	}
}
