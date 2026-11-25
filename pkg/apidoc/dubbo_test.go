package apidoc

import (
	"encoding/json"
	"strings"
	"testing"
)

const rpcDoc = `{"dubbo":"1.0","info":{"title":"RPC","version":"1"},"methods":[{"interface":"example.HouseService","method":"get","summary":"查询","parameters":[{"name":"id","java_type":"java.lang.Long","example":9223372036854775806}],"returns":{"java_type":"example.House","schema":{"$ref":"#/components/schemas/House"}}}],"components":{"schemas":{"House":{"type":"object","properties":{"id":{"type":"integer"}}}}}}`

func TestDubboIdentityAndSchemaDiff(t *testing.T) {
	fields, err := DocumentFields([]byte(rpcDoc))
	if err != nil || len(fields) != 2 {
		t.Fatal("native fields lost", fields, err)
	}
	d, ops, sum, e := Parse([]byte(rpcDoc))
	if e != nil || d.Protocol() != "dubbo" || len(ops) != 1 {
		t.Fatal(e, ops)
	}
	if !strings.Contains(string(ops[0].Definition), "9223372036854775806") {
		t.Fatal("precision lost")
	}
	var doc map[string]any
	json.Unmarshal([]byte(rpcDoc), &doc)
	original := d.Methods[0]
	other := original
	other.Parameters = []RPCParameter{{Name: "id", JavaType: "java.lang.String"}}
	if original.ID() == other.ID() {
		t.Fatal("overload collision")
	}
	other = original
	other.Group = "v2"
	if original.ID() == other.ID() {
		t.Fatal("group collision")
	}
	_, _, same, _ := Parse([]byte(" \n" + rpcDoc))
	if sum != same {
		t.Fatal("unstable digest")
	}
	after := strings.Replace(rpcDoc, `"id":{"type":"integer"}`, `"id":{"type":"string"}`, 1)
	diff, e := Compare([]byte(rpcDoc), []byte(after))
	if e != nil || len(diff.Changes) == 0 {
		t.Fatal("referenced schema change lost", e, diff)
	}
	for _, bad := range []string{strings.Replace(rpcDoc, "java.lang.Long", "void[]", 1), strings.Replace(rpcDoc, "example.HouseService", "../bad", 1), strings.Replace(rpcDoc, `"dubbo":"1.0"`, `"dubbo":"2.0"`, 1)} {
		if _, _, _, e := Parse([]byte(bad)); e == nil {
			t.Fatal("invalid contract accepted")
		}
	}
}
