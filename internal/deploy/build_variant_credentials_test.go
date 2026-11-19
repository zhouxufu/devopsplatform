package deploy

import (
	"strings"
	"testing"
)

func TestBuildVariantRepositoryCredentialReferences(t *testing.T) {
	for _, tc := range []struct {
		name, properties, connection string
		blocked                      bool
	}{
		{"empty", "", "", false},
		{"bound authentication", "aliyun.username=${env.GRADLE_REPOSITORY_USERNAME}\naliyun.password=${env.GRADLE_REPOSITORY_PASSWORD}", "dependencies", false},
		{"unbound authentication", "aliyun.password=${env.GRADLE_REPOSITORY_PASSWORD}", "", true},
		{"literal username", "aliyun.username=fixture", "dependencies", true},
		{"arbitrary variable", "stage=${env.BUILD_STAGE}", "dependencies", true},
		{"repository URL", "repository=${env.GRADLE_REPOSITORY_URL}", "dependencies", true},
		{"mixed with environment", "aliyun.username=${env.GRADLE_REPOSITORY_USERNAME}\nenv=test", "dependencies", true},
		{"malformed", "not-an-assignment", "dependencies", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, system := range []bool{false, true} {
				snapshot := &buildSnapshot{Delivery: ServiceDelivery{MavenConnection: tc.connection}}
				if system {
					snapshot.Service.GradleSystemProperties = tc.properties
				} else {
					snapshot.Service.GradleProjectProperties = tc.properties
				}
				if blocked := hasUnscopedGradleBuildProperties(snapshot); blocked != tc.blocked {
					t.Fatalf("system=%t: blocked=%t, expected=%t", system, blocked, tc.blocked)
				}
			}
		})
	}
}

func TestBuildVariantRepositoryAuthenticationAllowsRuntimeEnvironments(t *testing.T) {
	s, _, _ := variantFixture(t, "gradle", "image")
	setVariant(t, s, "test", BuildVariant{})
	setVariant(t, s, "prod", BuildVariant{})
	if err := s.UpsertConnection(ResourceConnection{Name: "dependencies", Kind: "maven", Endpoint: "https://repository.example.invalid"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertServiceDelivery(ServiceDelivery{Service: "orders", Runner: "local", MavenConnection: "dependencies"}); err != nil {
		t.Fatal(err)
	}
	sv := *s.GetService("orders")
	sv.GradleProjectProperties = "aliyun.username=${env.GRADLE_REPOSITORY_USERNAME}\naliyun.password=${env.GRADLE_REPOSITORY_PASSWORD}"
	if err := s.UpsertService(sv); err != nil {
		t.Fatal(err)
	}
	a := variantArtifact(t, s, "test")
	for _, env := range []string{"test", "prod"} {
		if err := s.validateArtifactBuildVariant(a, env); err != nil {
			t.Fatalf("repository authentication blocked common artifact in %s: %v", env, err)
		}
	}
	// Current or public labels cannot retrofit an explicit build contract.
	setVariant(t, s, "prod", BuildVariant{Name: "prod", GradleProjectProperties: "env=prod"})
	if err := s.validateArtifactBuildVariant(a, "prod"); err == nil || !strings.Contains(err.Error(), "不兼容") {
		t.Fatalf("environment contract bypassed: %v", err)
	}
	if err := s.validateArtifactBuildVariant(a, "test"); err == nil {
		t.Fatal("undeclared environment accepted after another environment declares a contract")
	}
	if len(s.ReleaseOrders("", 100)) != 0 || len(s.Deploys("orders", 100)) != 0 {
		t.Fatal("compatibility checks created release side effects")
	}
}
