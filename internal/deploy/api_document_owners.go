package deploy

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"logplatform/pkg/apidoc"
	"sort"
)

type DocumentMaintainer struct {
	Maintainer string `json:"maintainer"`
	Source     string `json:"source"`
}
type DocumentMaintainerChange struct {
	Operation string `json:"operation"`
	Before    string `json:"before"`
	After     string `json:"after"`
	Source    string `json:"source"`
}
type documentMaintainerReader interface{ QueryRow(string, ...any) *sql.Row }

func decodeDocumentMaintainers(raw string) (map[string]DocumentMaintainer, error) {
	if raw == "" {
		return nil, nil
	}
	var result map[string]DocumentMaintainer
	if json.Unmarshal([]byte(raw), &result) != nil || result == nil {
		return nil, fmt.Errorf("文档维护人快照无效")
	}
	return result, nil
}
func loadDocumentMaintainers(reader documentMaintainerReader, id int64) (map[string]DocumentMaintainer, error) {
	if id == 0 {
		return nil, nil
	}
	var raw string
	err := reader.QueryRow(`SELECT document FROM api_document_maintainer_maps WHERE snapshot_id=?`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeDocumentMaintainers(raw)
}

// Legacy snapshots retain exactly their original declaration/default rule.
// New snapshots contain every operation, including an intentionally unknown owner.
func applyFrozenDocumentMaintainers(ops []apidoc.Operation, owners map[string]DocumentMaintainer, fallback string) error {
	if owners == nil {
		applyDocumentMaintainer(ops, fallback)
		return nil
	}
	for i := range ops {
		owner, ok := owners[ops[i].ID]
		if !ok {
			return fmt.Errorf("文档接口 %s 缺少冻结维护人", ops[i].ID)
		}
		ops[i].Maintainer = owner.Maintainer
	}
	return nil
}
func nextDocumentMaintainers(ops, previous []apidoc.Operation, fallback string) map[string]DocumentMaintainer {
	old := map[string]string{}
	for _, op := range previous {
		old[op.ID] = op.Maintainer
	}
	owners := map[string]DocumentMaintainer{}
	for _, op := range ops {
		value := DocumentMaintainer{}
		if explicit := apidoc.ExplicitMaintainer(op); explicit != "" {
			value = DocumentMaintainer{explicit, "explicit"}
		} else if owner, exists := old[op.ID]; exists {
			value = DocumentMaintainer{owner, "inherited"}
		} else if fallback != "" {
			value = DocumentMaintainer{fallback, "mcp-key"}
		} else if op.Maintainer != "" {
			// A document-wide contact is not evidence of an operation's code author.
			// Verified MCP Key ownership takes precedence for new operations.
			value = DocumentMaintainer{op.Maintainer, "document"}
		} else {
			value.Source = "unspecified"
		}
		owners[op.ID] = value
	}
	return owners
}
func (s *Store) previewDocumentMaintainers(ops []apidoc.Operation, previous APIDocumentSnapshot, fallback string) (map[string]DocumentMaintainer, []DocumentMaintainerChange, error) {
	var old []apidoc.Operation
	if previous.ID > 0 {
		_, parsed, _, e := apidoc.Parse(previous.Document)
		if e != nil {
			return nil, nil, e
		}
		old = parsed
		owners, e := loadDocumentMaintainers(s.db, previous.ID)
		if e != nil {
			return nil, nil, e
		}
		if e = applyFrozenDocumentMaintainers(old, owners, previous.DefaultMaintainer); e != nil {
			return nil, nil, e
		}
	}
	owners := nextDocumentMaintainers(ops, old, fallback)
	before := map[string]string{}
	for _, op := range old {
		before[op.ID] = op.Maintainer
	}
	changes := []DocumentMaintainerChange{}
	for id, owner := range owners {
		prior, existed := before[id]
		if prior != owner.Maintainer || !existed {
			changes = append(changes, DocumentMaintainerChange{id, prior, owner.Maintainer, owner.Source})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Operation < changes[j].Operation })
	return owners, changes, nil
}
func documentMaintainerDigest(owners map[string]DocumentMaintainer) string {
	raw, _ := json.Marshal(owners)
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}
