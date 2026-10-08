package deploy

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestUpsertConnectionAcceptsChineseDisplayNameAndNormalizesEndpoint(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertConnection(ResourceConnection{
		Name:     "云效代码凭据",
		Kind:     "git",
		Endpoint: "codeup.aliyun.com",
		Username: "builder@example.com",
		Password: "token",
	}); err != nil {
		t.Fatalf("UpsertConnection: %v", err)
	}
	got := st.GetConnection("云效代码凭据")
	if got == nil {
		t.Fatal("connection was not saved")
	}
	if got.Endpoint != "https://codeup.aliyun.com" {
		t.Fatalf("Endpoint = %q", got.Endpoint)
	}
}

func testOpenSSHPrivateKey(t *testing.T) string {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(privateKey, "devopsplatform-test")
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(block))
}

func TestGitSSHConnectionSecretsAreEncryptedAndInjected(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	privateKey := testOpenSSHPrivateKey(t)
	connection := ResourceConnection{
		Name:       "云效-SSH",
		Kind:       "git",
		Endpoint:   "codeup.aliyun.com",
		Username:   "git",
		PrivateKey: privateKey,
		Config: map[string]string{
			"auth_mode":       "ssh",
			"ssh_fingerprint": "SHA256:example",
			"known_hosts":     "codeup.aliyun.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest",
		},
	}
	if err := st.UpsertConnection(connection); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := st.db.QueryRow(`SELECT secret FROM resource_connections WHERE name=?`, connection.Name).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "OPENSSH PRIVATE KEY") || !strings.HasPrefix(stored, "enc:v1:") {
		t.Fatalf("private key was not encrypted: %q", stored)
	}
	public := st.Connections()
	if len(public) != 1 || public[0].PrivateKey != "" || public[0].Passphrase != "" || public[0].Config["ssh_fingerprint"] == "" {
		t.Fatalf("public connection leaked secret or lost fingerprint: %#v", public)
	}
	if err := st.UpsertService(Service{Name: "api", GitURL: "git@codeup.aliyun.com:team/api.git", Workdir: t.TempDir(), ArtifactType: "jar", BuildTool: "maven", MavenExecutable: "mvn", MavenGoals: "package", ArtifactGlob: "target/*.jar"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertServiceDelivery(ServiceDelivery{Service: "api", Runner: "local", GitConnection: connection.Name}); err != nil {
		t.Fatal(err)
	}
	prepared, err := st.PrepareServiceResources(st.GetService("api"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(prepared.RuntimeEnv["GIT_SSH_PRIVATE_KEY"]) != strings.TrimSpace(privateKey) || prepared.RuntimeEnv["GIT_SSH_KNOWN_HOSTS"] != connection.Config["known_hosts"] || prepared.RuntimeEnv["GIT_PASSWORD"] != "" {
		t.Fatalf("SSH credential was not prepared correctly: %#v", prepared.RuntimeEnv)
	}
	if len(prepared.RuntimeSecrets) == 0 || strings.TrimSpace(prepared.RuntimeSecrets[0]) != strings.TrimSpace(privateKey) {
		t.Fatalf("private key was not registered for log redaction")
	}
}

func TestUpsertConnectionReportsNameAndKindSeparately(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertConnection(ResourceConnection{Name: "bad name", Kind: "git", Endpoint: "https://example.test"}); err == nil || !strings.Contains(err.Error(), "连接名称") {
		t.Fatalf("invalid-name error = %v", err)
	}
	if err := st.UpsertConnection(ResourceConnection{Name: "valid-name", Kind: "unknown", Endpoint: "https://example.test"}); err == nil || !strings.Contains(err.Error(), "连接类型") {
		t.Fatalf("invalid-kind error = %v", err)
	}
}
