package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type agentIdentity struct {
	ID      string   `json:"id"`
	Aliases []string `json:"aliases"`
}

// BindIdentity preserves the installation identity in checked atomic metadata.
func (s *Spool) BindIdentity(c *Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return os.ErrClosed
	}
	path := filepath.Join(s.dir, "identity.json")
	var identity agentIdentity
	err := readQueueJSON(path, &identity)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("invalid stored agent identity: %w", err)
	}
	if os.IsNotExist(err) {
		var random [16]byte
		if _, err = rand.Read(random[:]); err != nil {
			return err
		}
		identity.ID = "agent_" + hex.EncodeToString(random[:])
	}
	if !validPersistentID(identity.ID) {
		return fmt.Errorf("invalid stored agent identity; refusing to replace it")
	}
	name := strings.TrimSpace(c.AgentName)
	if name == "" {
		name = c.AgentID
	}
	if name == "" {
		name = c.Host
		if c.Environment != "" {
			name = c.Environment + "/" + name
		}
	}
	if len(name) > 256 || strings.ContainsAny(name, "\r\n\x00") {
		return fmt.Errorf("invalid agent display name")
	}
	seen := false
	for _, alias := range identity.Aliases {
		seen = seen || alias == name
	}
	if name != "" && name != identity.ID && !seen {
		identity.Aliases = append(identity.Aliases, name)
	}
	if len(identity.Aliases) > 32 {
		identity.Aliases = identity.Aliases[len(identity.Aliases)-32:]
	}
	if err = writeQueueJSON(path, identity); err != nil {
		return err
	}
	c.AgentID, c.AgentName, c.AgentAliases = identity.ID, name, identity.Aliases
	return nil
}

func validPersistentID(id string) bool {
	if !strings.HasPrefix(id, "agent_") || len(id) != 38 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(id, "agent_"))
	return err == nil
}

// ReadIdentity is read-only, including when another Agent process holds the
// spool open. Inspection must never create or rotate an installation identity.
func ReadIdentity(path string) (string, error) {
	if _, err := os.Stat(path + ".queue"); err == nil {
		var identity agentIdentity
		if err = readQueueJSON(filepath.Join(path+".queue", "identity.json"), &identity); err != nil {
			return "", err
		}
		if !validPersistentID(identity.ID) {
			return "", fmt.Errorf("invalid stored agent identity")
		}
		return identity.ID, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	db, err := legacyQueueDB(path)
	if err != nil {
		return "", err
	}
	defer db.Close()
	var id string
	if err = db.QueryRow(`SELECT id FROM agent_identity WHERE singleton=1`).Scan(&id); err != nil {
		return "", err
	}
	if !validPersistentID(id) {
		return "", fmt.Errorf("invalid stored agent identity")
	}
	return id, nil
}
