package appcatalog

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// Identifier is presentation metadata, never a replacement for an existing
// source key. Legacy Unicode keys keep their routes, snapshots and runtimes.
func Identifier(sourceKey string) string {
	if ValidIdentifier(sourceKey) {
		return sourceKey
	}
	digest := sha256.Sum256([]byte(sourceKey))
	return fmt.Sprintf("legacy-%x", digest[:20])
}

func ValidIdentifier(value string) bool { return identifierPattern.MatchString(value) }

func DisplayName(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func ValidateDisplayName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 100 {
		return "", fmt.Errorf("应用名称须为 1～100 个字符，支持中文")
	}
	for _, c := range value {
		if unicode.IsControl(c) {
			return "", fmt.Errorf("应用名称不能包含换行或控制字符")
		}
	}
	return value, nil
}
