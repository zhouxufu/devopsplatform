package deploy

import "testing"

func TestAPITestTargetsAcceptRegisteredHostAddressForms(t *testing.T) {
	st, _ := apiTestingFixture(t)
	sv := st.GetService("orders")
	env := st.GetServiceEnvironment("orders", "test")
	// Use explicit environment bindings, as on the affected application.
	env.HostGroup = ""
	env.Hosts = []string{"test-host"}
	env.DeployConfig = map[string]string{"java_port": "4010"}
	env.HealthURL = ""
	for _, tc := range []struct{ name, address, base string }{
		{"bare IPv4", "192.0.2.55", "http://192.0.2.55:4010"},
		{"explicit SSH port", "192.0.2.55:2222", "http://192.0.2.55:4010"},
		{"hostname", "service.example.test", "http://service.example.test:4010"},
		{"bare IPv6", "2001:db8::55", "http://[2001:db8::55]:4010"},
		{"bracketed IPv6", "[2001:db8::55]", "http://[2001:db8::55]:4010"},
		{"IPv6 SSH port", "[2001:db8::55]:2222", "http://[2001:db8::55]:4010"},
		{"invalid port", "192.0.2.55:bad", ""},
		{"URL is not host address", "http://192.0.2.55", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Seed directly so malformed legacy addresses are covered as well.
			if _, err := st.db.Exec(`UPDATE hosts SET addr=? WHERE name=?`, tc.address, "test-host"); err != nil {
				t.Fatal(err)
			}
			targets := st.apiTestTargets(sv, env, apiTestProfile{})
			if tc.base == "" {
				if len(targets) != 0 {
					t.Fatalf("invalid address produced targets: %+v", targets)
				}
				return
			}
			if len(targets) != 1 || targets[0].BaseURL != tc.base || targets[0].Host != "test-host" || targets[0].Key != "host:test-host:4010" {
				t.Fatalf("unexpected targets: %+v", targets)
			}
		})
	}
	if _, err := st.db.Exec(`UPDATE hosts SET addr='192.0.2.55' WHERE name='test-host'`); err != nil {
		t.Fatal(err)
	}
	env.DeployConfig = map[string]string{}
	if got := st.apiTestTargets(sv, env, apiTestProfile{}); len(got) != 0 {
		t.Fatalf("must not use default SSH port as an HTTP port: %+v", got)
	}
}
