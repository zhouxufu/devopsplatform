package deploytask

// EnrollmentScope is the non-secret policy proven by an installation's callback.
// Keep the digest independent of the credential so rotation preserves identity.
type EnrollmentScope struct {
	OperationID string   `json:"operation_id"`
	AgentID     string   `json:"agent_id"`
	Host        string   `json:"host"`
	Environment string   `json:"environment"`
	Roots       []string `json:"roots"`
	StateDir    string   `json:"state_dir"`
	Launcher    string   `json:"launcher"`
}

type EnrollmentProof struct {
	EnrollmentScope
	WorkerVerified bool `json:"worker_verified"`
}
