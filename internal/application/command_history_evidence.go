package application

import (
	"cyberagent-workbench/internal/runner"
	"unicode/utf8"
)

func sanitizeControlledCommandEvidence(data []byte) string {
	return runner.SanitizeCommandEvidence(data)
}

func truncateUTF8Bytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	suffix := "\n[TRUNCATED BY PRAYU]\n"
	target := limit - len(suffix)
	if target < 0 {
		target = 0
	}
	for target > 0 && !utf8.ValidString(value[:target]) {
		target--
	}
	return value[:target] + suffix
}
