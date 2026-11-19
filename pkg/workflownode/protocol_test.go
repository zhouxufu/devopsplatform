package workflownode

import (
	p "logplatform/pkg/extensionprotocol"
	"testing"
)

func TestRemoteDescriptorsBoundEffectsAndConfiguration(t *testing.T) {
	d := Descriptor{Version: 1, Key: "scan", Name: "只读检查", Executor: RemoteExecutor, Effect: "read-only", Configuration: "node-fields", Fields: []p.Field{{Key: "rule_ref", Label: "规则引用", Type: "string", Required: true}}}
	if err := ValidateDescriptor(d); err != nil {
		t.Fatal(err)
	}
	for _, effect := range []string{"", "sql-write", "deploy"} {
		v := d
		v.Effect = effect
		if ValidateDescriptor(v) == nil {
			t.Fatal("unreviewed write executor declared")
		}
	}
	v := d
	v.Executor = "bash"
	if ValidateDescriptor(v) == nil {
		t.Fatal("arbitrary script executor accepted")
	}
}

func TestSharedProviderRequiresInstalledWorkspaceAndDeclaresEffects(t *testing.T) {
	if SharedDescriptor(APITestProvider, "deploy") != nil {
		t.Fatal("uninstalled provider visible")
	}
	d := SharedDescriptor(APITestProvider, "deploy,api-center")
	if d == nil || ValidateDescriptor(*d) != nil || d.Effect != "http-request" || d.ProviderDigest == "" {
		t.Fatal(d)
	}
	bad := *d
	bad.Effect = "read-only"
	if ValidateDescriptor(bad) == nil {
		t.Fatal("test HTTP writes mislabeled read-only")
	}
}
