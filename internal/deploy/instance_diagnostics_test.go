package deploy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"text/template"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestDockerInstanceTemplateOptionalHealthAndLabels(t *testing.T) {
	// Docker inspect's strict missing-key behavior must be exercised rather
	// than stubbing already-rendered JSON, which hid the original defect.
	for _, tc := range []struct {
		name, health, labels string
		expectedHealth       string
	}{
		{"no-health-or-labels", "", "", ""},
		{"explicit-null", `,"Health":null`, `,"Labels":null`, ""},
		{"empty-labels", ``, `,"Labels":{}`, ""},
		{"healthy", `,"Health":{"Status":"healthy"}`, `,"Labels":{"io.devopsplatform.service":"api","io.devopsplatform.environment":"qa"}`, "healthy"},
		{"unhealthy", `,"Health":{"Status":"unhealthy"}`, `,"Labels":{"unrelated":"value"}`, "unhealthy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"Id":"` + strings.Repeat("a", 64) + `","Name":"/api","Image":"sha256:` + strings.Repeat("b", 64) + `","RestartCount":0,"Config":{"Image":"registry.invalid/api@sha256:abc"` + tc.labels + `},"State":{"Status":"running","Pid":42,"StartedAt":"2026-10-29T00:00:00Z"` + tc.health + `}}`
			var input any
			if err := json.Unmarshal([]byte(raw), &input); err != nil {
				t.Fatal(err)
			}
			format, err := template.New("docker").Option("missingkey=error").Funcs(template.FuncMap{"json": func(v any) string { b, _ := json.Marshal(v); return string(b) }}).Parse(dockerInstanceInspectTemplate())
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err = format.Execute(&out, input); err != nil {
				t.Fatal(err)
			}
			var result dockerInstanceInfo
			if err = json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err, out.String())
			}
			if result.State != "running" || result.Health != tc.expectedHealth || result.ID != strings.Repeat("a", 64) {
				t.Fatal(result)
			}
			if tc.name == "healthy" && (result.LabelService != "api" || result.LabelEnvironment != "qa") {
				t.Fatal(result)
			}
		})
	}
	cmd := exec.Command("bash", "-n")
	cmd.Stdin = strings.NewReader(dockerInstanceStatusCommand("api"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("invalid query shell: %v %s", err, out)
	}
}

func TestInstanceReadErrorsSeparateSSHAndDockerWithoutLeakingDetails(t *testing.T) {
	for _, tc := range []struct {
		output      string
		err         error
		state, code string
	}{
		{"template parsing error: map has no entry for key Health", &ssh.ExitError{}, "inspection-failed", "docker-template"},
		{"permission denied while trying to connect to Docker daemon socket", &ssh.ExitError{}, "inspection-failed", "docker-permission"},
		{"Cannot connect to the Docker daemon", fmt.Errorf("wrapped: %w", &ssh.ExitError{}), "inspection-failed", "docker-daemon"},
		{"docker: command not found", &ssh.ExitError{}, "inspection-failed", "docker-missing"},
		{"unknown flag: --no-trunc", &ssh.ExitError{}, "inspection-failed", "docker-command"},
		{"arbitrary stderr password=do-not-leak", &ssh.ExitError{}, "inspection-failed", "docker-query"},
		{"", errors.New("ssh: unable to authenticate; password=do-not-leak"), "unreachable", "ssh-auth"},
		{"", errors.New("knownhosts: key mismatch password=do-not-leak"), "unreachable", "ssh-host-key"},
		{"", &net.DNSError{IsTimeout: true, Name: "private-host"}, "unreachable", "ssh-timeout"},
	} {
		state, code, message := classifyInstanceReadError(tc.output, tc.err)
		if state != tc.state || code != tc.code {
			t.Fatalf("got %s/%s want %s/%s", state, code, tc.state, tc.code)
		}
		if strings.Contains(message, "do-not-leak") || strings.Contains(message, "private-host") {
			t.Fatal("raw diagnostic leaked")
		}
	}
}

func TestDockerTemplateFailureDoesNotReportDisconnectedHost(t *testing.T) {
	_, m, tg, sv, h, _ := applicationInstanceFixture(t)
	m.exec = func(Host, string, time.Duration) (string, error) {
		return "template parsing error: map has no entry for key Health", &ssh.ExitError{}
	}
	r := m.inspect(tg, sv, h)
	if r.State != "inspection-failed" || r.DiagnosticCode != "docker-template" || r.Controllable {
		t.Fatal(r)
	}
}
