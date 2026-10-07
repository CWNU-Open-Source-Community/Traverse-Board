package domain

import (
	"strings"
	"testing"
)

func TestNormalizeThreadTitleQueryPreservesLiteralUnicodeAndBounds(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"  FIND 中文_% Ä  ", "find 中文_% Ä"},
		{"\t \n", ""},
		{strings.Repeat("中", MaxThreadTitleQueryRunes), strings.Repeat("中", MaxThreadTitleQueryRunes)},
	} {
		got, err := NormalizeThreadTitleQuery(test.input)
		if err != nil || got != test.want {
			t.Fatalf("query=%q got=%q want=%q err=%v", test.input, got, test.want, err)
		}
	}
	for _, invalid := range []string{string([]byte{0xff}), strings.Repeat("中", MaxThreadTitleQueryRunes+1)} {
		if _, err := NormalizeThreadTitleQuery(invalid); err == nil {
			t.Fatalf("invalid query accepted: %q", invalid)
		}
	}
}
