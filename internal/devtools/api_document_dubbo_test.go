package devtools

import (
	"context"
	"strings"
	"testing"
)

func TestMCPDubboDocumentConfirmationUsesProtocolBaseline(t *testing.T) {
	s, tok, _, _ := documentMCPFixture(t)
	http := docConfirm(t, s, tok, docCall(t, s, tok, "report_api_document", reportArgs(reportDocument)))
	httpID := intArg(mapValue(http["result"]), "id", 0)
	const rpc = `{"dubbo":"1.0","info":{"title":"RPC","version":"1"},"methods":[{"interface":"example.HouseService","method":"get","summary":"查询房源","parameters":[{"name":"id","java_type":"java.lang.Long"}],"returns":{"java_type":"java.lang.String"}}]}`
	p := docCall(t, s, tok, "report_api_document", reportArgs(rpc))
	// An HTTP update must not invalidate an independent Dubbo preview.
	docConfirm(t, s, tok, docCall(t, s, tok, "report_api_document", reportArgs(strings.Replace(reportDocument, `"description":"ok"`, `"description":"HTTP changed"`, 1))))
	first := docConfirm(t, s, tok, p)
	id := intArg(mapValue(first["result"]), "id", 0)
	if id <= httpID {
		t.Fatal("Dubbo confirmation did not create its own revision", first)
	}
	args := map[string]any{"service": "orders", "environment": "test", "document_id": id, "protocol": "http", "patches": []any{map[string]any{"op": "replace", "path": "/methods/0/summary", "value": "查询房源详情"}}}
	patch := docCall(t, s, tok, "patch_api_document", args)
	next := intArg(mapValue(docConfirm(t, s, tok, patch)["result"]), "id", 0)
	if next <= id {
		t.Fatal("Dubbo patch did not use candidate protocol")
	}
	stale := docCall(t, s, tok, "report_api_document", reportArgs(strings.Replace(rpc, "查询房源", "过期修改", 1)))
	docConfirm(t, s, tok, docCall(t, s, tok, "report_api_document", reportArgs(strings.Replace(rpc, "查询房源", "最新修改", 1))))
	if _, _, _, e := s.Call(context.Background(), tok, "confirm_platform_action", map[string]any{"confirmation_id": mapValue(stale)["confirmation_id"], "confirm": true, "reason": "核对"}); e == nil {
		t.Fatal("stale Dubbo baseline accepted")
	}
	rows := docCall(t, s, tok, "list_api_documents", map[string]any{"service": "orders", "environment": "test", "protocol": "http"}).([]any)
	if len(rows) != 2 {
		t.Fatal("Dubbo writes changed HTTP history", rows)
	}
}
