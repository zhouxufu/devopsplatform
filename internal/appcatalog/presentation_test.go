package appcatalog

import (
	"strings"
	"testing"
)

func TestApplicationPresentationValidation(t *testing.T) {
	for _, v := range []string{"orders", "a1", "estate-ai-seek"} {
		if !ValidIdentifier(v) || Identifier(v) != v {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"中文", "../escape", "UPPER", "a_b", "a.b", "1abc", strings.Repeat("a", 64)} {
		if ValidIdentifier(v) {
			t.Fatal(v)
		}
	}
	if Identifier("旧中文应用") != Identifier("旧中文应用") || Identifier("旧中文应用") == Identifier("其他应用") || !ValidIdentifier(Identifier("旧中文应用")) {
		t.Fatal("unstable legacy identifier")
	}
	for _, v := range []string{"", " ", "bad\nname", strings.Repeat("中", 101)} {
		if _, err := ValidateDisplayName(v); err == nil {
			t.Fatal(v)
		}
	}
	if v, err := ValidateDisplayName(" 智能搜索服务 "); err != nil || v != "智能搜索服务" {
		t.Fatal(v, err)
	}
}
