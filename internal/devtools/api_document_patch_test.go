package devtools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPDocumentPatchFrozenCASAndScopedReads(t *testing.T) {
	s, tok, _, _ := documentMCPFixture(t)
	first := docConfirm(t, s, tok, docCall(t, s, tok, "report_api_document", reportArgs(reportDocument)))
	id := mapValue(first["result"])["id"]
	if id == nil {
		t.Fatal(first)
	}
	args := map[string]any{"service": "orders", "environment": "test", "document_id": id, "patches": []any{map[string]any{"op": "add", "path": "/paths/~1orders/get/summary", "value": "订单查询"}}}
	p := docCall(t, s, tok, "patch_api_document", args)
	current := mapValue(docCall(t, s, tok, "get_api_document", map[string]any{"service": "orders", "environment": "test", "document_id": id}))
	b, _ := json.Marshal(current["document"])
	if strings.Contains(string(b), "订单查询") {
		t.Fatal("preview wrote document")
	}
	args["patches"].([]any)[0].(map[string]any)["value"] = "冻结后篡改"
	result := docConfirm(t, s, tok, p)
	next := mapValue(result["result"])["id"]
	if next == nil {
		t.Fatal(result)
	}
	read := map[string]any{"service": "orders", "environment": "test", "document_id": next, "operation": "get /orders"}
	single := docCall(t, s, tok, "get_api_operation", read)
	if stringArg(mapValue(mapValue(single)["operation"]), "maintainer") != "alice" {
		t.Fatal("single-operation read lost frozen maintainer", single)
	}
	raw, _ := json.Marshal(single)
	if !strings.Contains(string(raw), "订单查询") || strings.Contains(string(raw), "冻结后篡改") {
		t.Fatal(string(raw))
	}
	addArgs := map[string]any{"service": "orders", "environment": "test", "document_id": next, "patches": []any{map[string]any{"op": "add", "path": "/paths/~1new", "value": map[string]any{"get": map[string]any{"responses": map[string]any{"200": map[string]any{"description": "new"}}}}}}}
	newResult := docConfirm(t, s, tok, docCall(t, s, tok, "patch_api_document", addArgs))
	next = mapValue(newResult["result"])["id"]
	newOp := mapValue(docCall(t, s, tok, "get_api_operation", map[string]any{"service": "orders", "environment": "test", "document_id": next, "operation": "get /new"}))
	if stringArg(mapValue(newOp["operation"]), "maintainer") != "alice" {
		t.Fatal("new partial-report operation missing verified Key owner", newOp)
	}
	staleArgs := map[string]any{"service": "orders", "environment": "test", "document_id": next, "patches": []any{map[string]any{"op": "replace", "path": "/paths/~1orders/get/summary", "value": "后续"}}}
	stale := docCall(t, s, tok, "patch_api_document", staleArgs)
	another := reportArgs(strings.Replace(reportDocument, `"responses"`, `"summary":"外部更新","responses"`, 1))
	docConfirm(t, s, tok, docCall(t, s, tok, "report_api_document", another))
	if _, _, _, e := s.Call(context.Background(), tok, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(stale)["confirmation_id"], "confirm": true, "reason": "核对"}); e == nil {
		t.Fatal("stale patch accepted")
	}
	for _, name := range []string{"get_api_operation", "get_work_item_api_changes", "search_api_operations", "patch_api_document"} {
		a := map[string]any{"service": "other", "environment": "test", "document_id": next, "operation": "get /orders", "work_item_id": 1, "q": "订单", "patches": staleArgs["patches"]}
		if _, _, _, e := s.Call(context.Background(), tok, name, a); e == nil {
			t.Fatal("scope widened", name)
		}
	}
	member := *tok
	member.role = "member"
	if _, _, _, e := s.Call(context.Background(), &member, "patch_api_document", staleArgs); e == nil {
		t.Fatal("member wrote")
	}
	search := docCall(t, s, tok, "search_api_operations", map[string]any{"service": "orders", "environment": "test", "q": "外部更新"})
	raw, _ = json.Marshal(search)
	if !strings.Contains(string(raw), "外部更新") {
		t.Fatal(string(raw))
	}
}
