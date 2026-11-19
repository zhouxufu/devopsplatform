package logtext

import "testing"

func TestStripANSIColorsPreservesContent(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain 中文\n\tstack", "plain 中文\n\tstack"},
		{"\x1b[32m2026-11-18 08:21:59 943\x1b[0;39m  \x1b[34mINFO \x1b[0;39m", "2026-11-18 08:21:59 943  INFO "},
		{"\x1b[1;35mp6spy\x1b[m : select 1", "p6spy : select 1"},
		{"\x1b[38:2:255:0:0m中文\x1b[0m\n\tat Example.run", "中文\n\tat Example.run"},
		{`literal \u001b[32m and \x1b[0m`, `literal \u001b[32m and \x1b[0m`},
		{"incomplete \x1b[32", "incomplete \x1b[32"},
		{"cursor \x1b[2K text", "cursor \x1b[2K text"},
	} {
		if got := StripANSIColors(tc.in); got != tc.want {
			t.Errorf("got %q want %q", got, tc.want)
		}
	}
}
