package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildEnvironmentPersistenceAndCompatibility(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sv := Service{Name: "api", ArtifactType: "jar", BuildTool: "gradle", GradleTasks: "build", Workdir: t.TempDir()}
	if err := st.UpsertService(sv); err != nil {
		t.Fatal(err)
	}
	e := &BuildEnvironment{Mode: "container", Preset: "gradle-jdk21"}
	if err := st.UpsertServiceDelivery(ServiceDelivery{Service: "api", BuildEnvironment: e}); err != nil {
		t.Fatal(err)
	}
	// Old clients omitting the field must not turn isolation off.
	if err := st.UpsertServiceDelivery(ServiceDelivery{Service: "api"}); err != nil {
		t.Fatal(err)
	}
	got := st.GetServiceDelivery("api")
	if got.BuildEnvironment.Preset != e.Preset {
		t.Fatalf("lost environment: %#v", got)
	}
	if all := st.AllServiceDelivery(); len(all) != 1 || all[0].BuildEnvironment.Preset != e.Preset {
		t.Fatal(all)
	}
	prepared, err := st.PrepareServiceResources(st.GetService("api"))
	if err != nil || !prepared.BuildEnvironment.isolated() {
		t.Fatal(err)
	}
	if err := st.UpsertServiceDelivery(ServiceDelivery{Service: "api", BuildEnvironment: &BuildEnvironment{Mode: "local"}}); err != nil {
		t.Fatal(err)
	}
	if st.GetServiceDelivery("api").BuildEnvironment.isolated() {
		t.Fatal("could not disable isolation")
	}
	for _, bad := range []BuildEnvironment{{Mode: "bad"}, {Mode: "container", Preset: "oops"}, {Mode: "container", Preset: "custom", Image: "builder"}, {Mode: "container", Preset: "custom", Image: "--privileged"}, {Mode: "container", Preset: "maven-jdk21"}, {Mode: "container", Preset: "gradle-jdk21", Network: "private"}} {
		if validateBuildEnvironment(&bad, &sv) == nil {
			t.Errorf("accepted invalid environment %+v", bad)
		}
	}
}

func TestContainerArgumentsKeepSecretsAndHostEnvironmentOut(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	output := filepath.Join(root, "output")
	for _, dir := range []string{work, output} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	def := &StepDef{Dir: root, Params: []StepParam{{Name: "COMMAND"}}}
	sv := &Service{RuntimeEnv: map[string]string{"DEPENDENCY_PASSWORD": "very-secret", "GIT_PASSWORD": "git-secret", "GIT_SSH_PRIVATE_KEY": "ssh-private-secret"}}
	env := def.BuildEnv(StepInst{}, work, "test", "v1", output)
	env = append(env, "LP_COMMAND=echo ok", "DEPENDENCY_PASSWORD=very-secret", "GIT_PASSWORD=git-secret", "GIT_SSH_PRIVATE_KEY=ssh-private-secret", "AWS_SECRET_ACCESS_KEY=host-secret")
	args, child, err := containerBuildArgs(sv, def, env, root, "test-container", "sha256:test")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\n")
	for _, v := range []string{"very-secret", "git-secret", "ssh-private-secret", "host-secret", "AWS_SECRET_ACCESS_KEY", "GIT_PASSWORD", "GIT_SSH_PRIVATE_KEY", "docker.sock", "--privileged"} {
		if strings.Contains(joined, v) {
			t.Errorf("leaked %s into container args", v)
		}
	}
	if !strings.Contains(joined, "DEPENDENCY_PASSWORD") || !strings.Contains(strings.Join(child, "\n"), "DEPENDENCY_PASSWORD=very-secret") {
		t.Fatal("missing explicit secret injection")
	}
	sv.BuildEnvironment = &BuildEnvironment{Mode: "container", Preset: "gradle-jdk21", Network: "host"}
	args, _, err = containerBuildArgs(sv, def, env, root, "test-host-network", "sha256:test")
	if err != nil || !strings.Contains(strings.Join(args, " "), "--network host") {
		t.Fatalf("host build network not applied: %v %#v", err, args)
	}
	sv.RuntimeEnv["JAVA_HOME"] = "/host/jdk"
	if _, _, err := containerBuildArgs(sv, def, env, root, "test", "image"); err == nil {
		t.Fatal("allowed host JAVA_HOME override")
	}
}

func TestGradleWrapperCompatibilityPreflight(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "gradle/wrapper"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gradlew"), []byte("#!/bin/sh"), 0755); err != nil {
		t.Fatal(err)
	}
	sv := &Service{BuildTool: "gradle", GradleExecutable: "auto", BuildEnvironment: &BuildEnvironment{Mode: "container", Preset: "gradle-jdk21"}}
	for _, tc := range []struct {
		version, preset string
		ok              bool
	}{{"8.4", "gradle-jdk21", false}, {"8.5", "gradle-jdk21", true}, {"7.2", "gradle-jdk17", false}, {"7.3", "gradle-jdk17", true}, {"9.0", "gradle-jdk8", false}, {"8.14.3", "gradle-jdk8", true}} {
		sv.BuildEnvironment.Preset = tc.preset
		if err := os.WriteFile(filepath.Join(root, "gradle/wrapper/gradle-wrapper.properties"), []byte("distributionUrl=https\\://services.gradle.org/distributions/gradle-"+tc.version+"-bin.zip"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := checkGradleWrapperCompatibility(sv, root); (err == nil) != tc.ok {
			t.Errorf("%s / %s: %v", tc.version, tc.preset, err)
		}
	}
}

// Opt-in real Docker smoke test, using an already available trusted image.
func TestIsolatedBuildRealContainer(t *testing.T) {
	image := os.Getenv("DEVOPS_TEST_BUILD_IMAGE")
	if image == "" {
		t.Skip("set DEVOPS_TEST_BUILD_IMAGE for real Docker test")
	}
	root := t.TempDir()
	work := filepath.Join(root, "work")
	output := filepath.Join(root, "output")
	step := filepath.Join(root, "step")
	for _, dir := range []string{work, output, step} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/bash
set -eu
cd "$LP_WORKSPACE"
test "$TEST_SECRET" = "isolated-test-value"
test -z "${HOST_SHOULD_NOT_LEAK:-}"
test ! -e /var/run/docker.sock
cat > Smoke.java <<'JAVA'
public class Smoke { public static void main(String[] args) { System.out.println("java-smoke-ok"); } }
JAVA
javac Smoke.java
java Smoke
node -e 'require("fs").writeFileSync("node-result.txt", "node-smoke-ok")'
mkdir -p "$HOME/.gradle"
echo cache-ok > "$HOME/.gradle/smoke"
`
	if err := os.WriteFile(filepath.Join(step, "run.sh"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOST_SHOULD_NOT_LEAK", "do-not-forward")
	def := &StepDef{ID: "smoke", Kind: "build", Dir: step}
	sv := &Service{Name: "smoke", BuildEnvironment: &BuildEnvironment{Mode: "container", Preset: "custom", Image: image}, RuntimeEnv: map[string]string{"TEST_SECRET": "isolated-test-value"}}
	b := &Builder{workspaceDir: root}
	env := append(def.BuildEnv(StepInst{}, work, "smoke", "v1", output), "TEST_SECRET=isolated-test-value")
	var logs strings.Builder
	if code := b.execBuildStep(sv, def, env, func(s string) { logs.WriteString(s) }); code != 0 {
		t.Fatalf("exit %d\n%s", code, logs.String())
	}
	if data, err := os.ReadFile(filepath.Join(work, "node-result.txt")); err != nil || string(data) != "node-smoke-ok" {
		t.Fatalf("missing host-visible output: %v", err)
	}
	if !strings.Contains(logs.String(), "java-smoke-ok") {
		t.Fatal(logs.String())
	}
	// The image lookup and cleanup also run through actual Docker, not a mock.
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal(err)
	}
}

func TestContainerPipelinePackagesArtifact(t *testing.T) {
	image := os.Getenv("DEVOPS_TEST_BUILD_IMAGE")
	if image == "" {
		t.Skip("set DEVOPS_TEST_BUILD_IMAGE for real pipeline test")
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "source")
	if err := os.Mkdir(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "Smoke.java"), []byte(`public class Smoke { public static void main(String[] args) { System.out.println("ok"); } }`), 0644); err != nil {
		t.Fatal(err)
	}
	st, err := Open(filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, args := range [][]string{{"init", workspace}, {"-C", workspace, "add", "Smoke.java"}, {"-C", workspace, "-c", "user.name=DevOps Test", "-c", "user.email=test@example.invalid", "commit", "-m", "test fixture"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("create local test repository: %v %s", err, out)
		}
	}
	sv := Service{Name: "container-pipeline", GitURL: workspace, ArtifactType: "jar", BuildTool: "command", BuildCmd: "javac Smoke.java && jar cfe smoke.jar Smoke Smoke.class", ArtifactGlob: "smoke.jar"}
	if err := st.UpsertService(sv); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertServiceDelivery(ServiceDelivery{Service: sv.Name, BuildEnvironment: &BuildEnvironment{Mode: "container", Preset: "custom", Image: image}}); err != nil {
		t.Fatal(err)
	}
	hub := NewLogHub()
	b := NewBuilder(st, filepath.Join(root, "data"), hub)
	pr := NewPipelineRunner(st, b, hub, NewDeployer(st, hub, ""), NewStepLib("../../steps"))
	prepared, err := st.PrepareServiceResources(st.GetService(sv.Name))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err = serviceForPipelineRun(prepared, PipelineRunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	run := &PipelineRun{Service: sv.Name, Actor: "test", StartedAt: time.Now().Unix()}
	if err := st.CreateRun(run); err != nil {
		t.Fatal(err)
	}
	pr.run(run.ID, prepared, false)
	got := st.GetRun(run.ID)
	if got.Status != "success" || got.ArtifactID == 0 || got.ArtifactDigest == "" {
		t.Fatalf("pipeline failed: %#v\n%s", got, st.RunLog(run.ID))
	}
	a := st.Artifacts(sv.Name, 10)
	if len(a) != 1 || a[0].Kind != ArtifactJar {
		t.Fatalf("no archived JAR: %#v", a)
	}
	if _, err := os.Stat(a[0].URI); err != nil {
		t.Fatal(err)
	}
}
