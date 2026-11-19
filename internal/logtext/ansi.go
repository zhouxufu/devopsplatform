package logtext

import (
	"regexp"
	"strings"
)

// Only remove terminal styling (SGR), preserving text, whitespace and newlines.
// The common uncolored path does not allocate. Incomplete or literal escaped
// sequences are kept as content, rather than interpreting terminal commands.
var ansiColor = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

func StripANSIColors(text string) string {
	if strings.IndexByte(text, '\x1b') < 0 {
		return text
	}
	return ansiColor.ReplaceAllString(text, "")
}
