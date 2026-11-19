package deploy

import (
	"context"
	"encoding/json"
	"logplatform/pkg/extensionclient"
	p "logplatform/pkg/extensionprotocol"
	"logplatform/pkg/workflownode"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRemoteWorkflowTasksNeverResendUnknownStarts(t *testing.T) {
	starts, statusCalls := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Request p.Request `json:"request"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		var request workflownode.JobRequest
		json.Unmarshal(in.Request.Input, &request)
		if in.Request.ExpectedVersion != "v1" || in.Request.ExpectedDigest != "digest1" {
			t.Error("package identity not pinned")
		}
		switch in.Request.Operation {
		case "job.start":
			starts++
			json.NewEncoder(w).Encode(p.Response{Error: &p.Error{Code: "unavailable", Message: "fixture ambiguous startup"}})
		case "job.status":
			statusCalls++
			data, _ := json.Marshal(workflownode.JobStatus{ExecutionID: request.ExecutionID, State: "success", Message: "fixture complete"})
			json.NewEncoder(w).Encode(p.Response{Data: data})
		}
	}))
	defer srv.Close()
	file := filepath.Join(t.TempDir(), "broker.json")
	raw, _ := json.Marshal(extensionclient.Config{URL: srv.URL, Token: "fixture-broker-token-0123456789012345"})
	os.WriteFile(file, raw, 0600)
	t.Setenv(extensionclient.ConfigEnv, file)
	b := &workflownode.Binding{Provider: "scan", NodeKey: "check", ProviderVersion: "v1", ProviderDigest: "digest1", Environment: "test"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	id := workflowExecutionID(12, "check")
	unknown, err := runWorkflowJob(ctx, b, id)
	if !unknown || err == nil || starts != 1 {
		t.Fatal("uncertain request automatically retried")
	}
	status, err := invokeWorkflowJob(ctx, b, id, "job.status")
	if err != nil || status.State != "success" || starts != 1 || statusCalls != 1 {
		t.Fatal("reconciliation should only read status")
	}
}

func TestInstalledStyleRemoteNodeUsesGenericExecutorAndOneStart(t *testing.T) {
	var starts atomic.Int32
	digest := strings.Repeat("c", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Request p.Request `json:"request"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			w.WriteHeader(400)
			return
		}
		var data any
		if in.Request.Operation == "describe" {
			data = workflownode.Descriptor{Version: 1, Key: "check", Name: "只读检查", Executor: workflownode.RemoteExecutor, Effect: "read-only", Configuration: "node-fields", Fields: []p.Field{{Key: "rule_ref", Label: "规则引用", Type: "string", Required: true}}}
		} else {
			if in.Request.ExpectedVersion != "1.0.0" || in.Request.ExpectedDigest != digest {
				t.Error("job package identity not frozen")
			}
			var request workflownode.JobRequest
			_ = json.Unmarshal(in.Request.Input, &request)
			if request.Parameters["rule_ref"] != "fixture-rule" || request.Environment != "test" || len(request.Targets) != 1 || request.Targets[0].Service != "orders" {
				t.Error("approved scope was lost")
			}
			state := "success"
			if in.Request.Operation == "job.start" {
				starts.Add(1)
				state = "queued"
			}
			data = workflownode.JobStatus{ExecutionID: request.ExecutionID, State: state, Message: "fixture-only check"}
		}
		raw, _ := json.Marshal(data)
		_ = json.NewEncoder(w).Encode(p.Response{Data: raw, Provider: &p.ProviderInfo{ID: "fixture-check", Version: "1.0.0", Digest: digest}})
	}))
	defer srv.Close()
	file := filepath.Join(t.TempDir(), "broker.json")
	raw, _ := json.Marshal(extensionclient.Config{URL: srv.URL, Token: "fixture-broker-token-0123456789012345"})
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(extensionclient.ConfigEnv, file)
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertService(Service{Name: "orders", ArtifactType: "jar"}); err != nil {
		t.Fatal(err)
	}
	node := DeliveryNode{ID: "check", Name: "只读检查", Kind: "extension", Extension: &workflownode.Binding{Provider: "fixture-check", NodeKey: "check", Parameters: map[string]string{"rule_ref": "fixture-rule"}}}
	binding, err := st.prepareWorkflowExtension(node, []DeliveryService{{Service: "orders", Artifact: Artifact{ID: 41, Digest: "sha256:fixture"}}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if binding.Executor != workflownode.RemoteExecutor || binding.ProviderDigest != digest {
		t.Fatal("generic descriptor not frozen")
	}
	node.Extension = binding
	planRaw, _ := json.Marshal(DeliveryPlan{Nodes: []DeliveryNode{node}})
	stepsRaw, _ := json.Marshal([]DeliveryStep{{NodeID: node.ID, Name: node.Name, Kind: "extension", Status: "pending"}})
	result, err := st.db.Exec(`INSERT INTO delivery_runs(plan,digest,status,execution,env_secrets,owner,approver,created_at,revision) VALUES(?,'fixture','approved',?,'{}','fixture','',?,1)`, string(planRaw), string(stepsRaw), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	run := st.DeliveryRun(id)
	st.pipelineRunner = &PipelineRunner{}
	if err := st.advanceWorkflowExtension(run, &run.Steps[0], node, "fixture"); err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(6 * time.Second); time.Now().Before(until); {
		latest := st.DeliveryRun(id)
		if latest.Steps[0].Status != "executing" {
			if latest.Steps[0].Status != "success" {
				t.Fatal(latest.Steps[0].Detail)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st.DeliveryRun(id).Steps[0].Status != "success" || starts.Load() != 1 {
		t.Fatal("generic node did not complete exactly once")
	}
}
