package workflownode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Shared providers use the host's existing executor. Installing a workspace does
// not require a descriptor-only process. External providers keep the same contract.
const SharedWorkspacesEnv = "DEVOPSPLATFORM_SHARED_WORKSPACES"
const APITestExecutor = "api.test.execute.v1"
const APITestProvider = "builtin:api-center"

func SharedDescriptor(provider, installed string) *Descriptor {
	if provider != APITestProvider {
		return nil
	}
	found := false
	for _, name := range strings.Split(installed, ",") {
		if name == "api-center" {
			found = true
		}
	}
	if !found {
		return nil
	}
	d := &Descriptor{Version: 1, Key: "regression", Name: "执行接口测试集", Executor: APITestExecutor, Configuration: "application-binding", Effect: "http-request", BindingPath: "/test-execution", BindingLabel: "配置交付回归", ProviderVersion: "1"}
	raw, _ := json.Marshal(d)
	sum := sha256.Sum256(raw)
	d.ProviderDigest = hex.EncodeToString(sum[:])
	return d
}

type TestTask struct {
	VariablesDigest     string           `json:"variables_digest"`
	Service             string           `json:"service"`
	ApplicationID       string           `json:"application_id"`
	Environment         string           `json:"environment"`
	ArtifactID          int64            `json:"artifact_id"`
	ArtifactDigest      string           `json:"artifact_digest"`
	SuiteID             int64            `json:"suite_id"`
	SuiteRevision       int64            `json:"suite_revision"`
	CaseRevisions       map[string]int64 `json:"case_revisions"`
	ProfileRevision     int64            `json:"profile_revision"`
	TargetKey           string           `json:"target_key"`
	TargetConfiguration string           `json:"target_configuration"`
}
