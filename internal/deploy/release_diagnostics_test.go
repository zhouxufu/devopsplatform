package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReleaseDiagnosticsPersistedEvidence(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	encrypted, _, err := store.encryptRuntimeEnvironment(map[string]string{"RUNTIME_KEY": "private-fixture-value"})
	if err != nil {
		t.Fatal(err)
	}
	order := &ReleaseOrder{Service: "orders", Environment: "dev", Status: "failed", Reason: "publish request, not the error", runtimeEnvEncrypted: encrypted}
	if err := store.CreateReleaseOrder(order); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/releases/{id}/diagnostics", store.releaseDiagnostics)
	read := func(query string, status int) map[string]any {
		t.Helper()
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest("GET", fmt.Sprintf("/api/releases/%d/diagnostics?%s", order.ID, query), nil))
		body := recorder.Body.String()
		if recorder.Code != status || strings.Contains(body, "private-fixture-value") || strings.Contains(body, "fake.bearer.token") || strings.Contains(body, order.Reason) {
			t.Fatalf("unexpected diagnostic %d: %s", recorder.Code, body)
		}
		var report map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		return report
	}
	const scope = "service=orders&environment=dev"
	if read(scope, 200)["evidence_note"] == nil {
		t.Fatal("historical failure without evidence was not identified")
	}
	store.Audit("operator", "fail", "release", fmt.Sprint(order.ID), map[string]any{"error": "dispatch failed: private-fixture-value"})
	if read(scope, 200)["release_error"] != "dispatch failed: [REDACTED]" {
		t.Fatal("missing redacted pre-dispatch failure")
	}
	for i := 0; i < 6; i++ {
		target := &Deploy{Service: order.Service, Environment: order.Environment, Host: fmt.Sprintf("host-%d", i)}
		if err := store.CreateDeploy(target); err != nil {
			t.Fatal(err)
		}
		if err := store.AttachReleaseDeploys(order.ID, 1, []int64{target.ID}); err != nil {
			t.Fatal(err)
		}
		store.AppendDeployLog(target.ID, "private-fixture-value\nAuthorization: Bearer fake.bearer.token\n▶ 检查运行 JDK\nSelected Java is not executable\n✗ 检查运行 JDK失败: Process exited with status 2")
		if err := store.FinishDeploy(target.ID, "failed"); err != nil {
			t.Fatal(err)
		}
	}
	read("service=orders&environment=test", 404)
	read("service=other&environment=dev", 404)
	first := read(scope, 200)
	targets := first["targets"].([]any)
	if len(targets) != 5 || !strings.Contains(targets[0].(map[string]any)["log_tail"].(string), "Selected Java is not executable") || targets[0].(map[string]any)["reported_error"] != "检查运行 JDK失败: Process exited with status 2" {
		t.Fatal("failed host logs unavailable")
	}
	last := read(scope+fmt.Sprintf("&after_deploy_id=%.0f", first["next_after_deploy_id"]), 200)
	rows := last["targets"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["host"] != "host-5" || last["next_after_deploy_id"] != float64(0) {
		t.Fatal("lost or duplicated failure on second page")
	}
}

func TestRollbackFailurePreservesBothCauses(t *testing.T) {
	target := diagnosticTarget{Status: "rollback-failed", Log: "▶ 验证进程及服务就绪\nCaused by: java.sql.SQLException: database unavailable\nCaused by: java.net.SocketTimeoutException: Connect timed out\n✗ 验证进程及服务就绪失败: status 20\n▶ 恢复旧制品和旧环境变量\nCaused by: java.net.ConnectException: Connection refused\n✗ 恢复旧制品和旧环境变量失败: status 20\n✗ 部署结束：rollback-failed"}
	target.collectFailureDetails()
	if target.ReportedError != "验证进程及服务就绪失败: status 20" || target.FailureCause != "java.net.SocketTimeoutException: Connect timed out" || target.RollbackError != "恢复旧制品和旧环境变量失败: status 20" || target.RollbackCause != "java.net.ConnectException: Connection refused" {
		t.Fatalf("mixed failure evidence: %+v", target)
	}
}
