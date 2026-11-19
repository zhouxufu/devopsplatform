package server

import (
	"logplatform/internal/types"
	"testing"
	"time"
)

func TestAgentStableIdentityRenameAndLegacyMigration(t *testing.T) {
	a := &APIServer{agents: map[string]*AgentStat{}}
	id := "agent_11111111111111111111111111111111"
	a.trackAgent(types.IngestRequest{AgentID: "test/old"}, 9)
	a.agents["test/old"].LastSeen = time.Now().Unix() - 60
	a.trackAgent(types.IngestRequest{AgentID: id, AgentName: "test/new", AgentAliases: []string{"test/old", "test/new"}, QueueDepth: 2}, 3)
	if len(a.agents) != 1 || a.agents[id].Events != 12 || a.agents[id].Name != "test/new" || a.agents[id].QueueDepth != 2 {
		t.Fatalf("migration: %+v", a.agents)
	}
	a.trackAgent(types.IngestRequest{AgentID: id, AgentName: "test/newer", AgentAliases: []string{"test/old", "test/new", "test/newer"}}, 0)
	if len(a.agents) != 1 || a.agents[id].Events != 12 || a.agents[id].Name != "test/newer" {
		t.Fatal("rename created duplicate or reset counters")
	}
}

func TestAgentMigrationDoesNotMergeLiveLegacyOrStableInstallations(t *testing.T) {
	a := &APIServer{agents: map[string]*AgentStat{}}
	id1 := "agent_11111111111111111111111111111111"
	id2 := "agent_22222222222222222222222222222222"
	a.trackAgent(types.IngestRequest{AgentID: "test/same"}, 9)
	a.trackAgent(types.IngestRequest{AgentID: id1, AgentName: "test/same", AgentAliases: []string{"test/same"}}, 3)
	if len(a.agents) != 2 {
		t.Fatal("live legacy sender was merged")
	}
	a.trackAgent(types.IngestRequest{AgentID: id2, AgentName: "test/same", AgentAliases: []string{"test/same"}}, 5)
	a.agents["test/same"].LastSeen = time.Now().Unix() - 60
	a.trackAgent(types.IngestRequest{AgentID: id1, AgentName: "test/renamed", AgentAliases: []string{"test/same"}}, 0)
	if len(a.agents) != 3 || a.agents[id1].Events != 3 || a.agents[id2].Events != 5 {
		t.Fatal("ambiguous alias/stable senders were merged")
	}
	// Unrelated offline legacy records must not disappear by name heuristics.
	a.trackAgent(types.IngestRequest{AgentID: "test/unrelated"}, 1)
	a.agents["test/unrelated"].LastSeen = time.Now().Unix() - 100
	a.trackAgent(types.IngestRequest{AgentID: id1, AgentName: "test/renamed", AgentAliases: []string{"test/unique"}}, 0)
	if a.agents["test/unrelated"] == nil {
		t.Fatal("unrelated history deleted")
	}
}
