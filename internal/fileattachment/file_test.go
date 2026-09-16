package fileattachment

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFileProjectionSeparatesTextFromStoredDocuments(t *testing.T) {
	for _, tc := range []struct{ name, mime, body, want string }{
		{"empty.txt", "text/plain", "", "text"},
		{"code.go", "application/octet-stream", "package main\n// 中文内容\n", "text"},
		{"data.json", "application/json", "{\"name\":\"中文\"}", "text"},
		{"report.pdf", "application/pdf", "%PDF-1.7 (NOT_PARSED) Tj", "stored_only"},
		{"report.docx", "application/octet-stream", "PK\x03\x04ASCII_OFFICE", "stored_only"},
		{"archive.zip", "application/zip", "ASCII_ZIP", "stored_only"},
		{"mislabel.txt", "text/plain", "%PDF-1.7 (NOT_PARSED) Tj", "stored_only"},
		{"binary.bin", "application/octet-stream", "\xff\xfeabc", "stored_only"},
		{"controls.txt", "text/plain", "hello\x00world", "stored_only"},
	} {
		t.Run(tc.name+tc.want, func(t *testing.T) {
			v, text, err := Validate(tc.name, tc.mime, []byte(tc.body))
			if err != nil || v.Readability != tc.want || v.ByteSize != len(tc.body) || v.SHA256 != digest([]byte(tc.body)) {
				t.Fatalf("%#v %v", v, err)
			}
			if tc.want == "stored_only" && (text != "" || v.Reason == "" || v.TextSHA256 != "") {
				t.Fatal("stored document falsely projected text")
			}
			if tc.want == "text" && (text != tc.body || v.TextSHA256 != digest([]byte(text))) {
				t.Fatal("text bytes changed")
			}
		})
	}
}

func TestFileTextBoundedAtUTF8BoundaryAndRedactedBeforeClipping(t *testing.T) {
	body := strings.Repeat("中", MaxTextBytes/3) + "API_KEY=sk-" + strings.Repeat("a", 40) + "\n"
	v, text, err := Validate("limit.md", "text/markdown", []byte(body))
	if err != nil || v.Readability != "partial_text" || !v.Redacted || !utf8.ValidString(text) || len(text) > MaxTextBytes || strings.Contains(text, "sk-") {
		t.Fatalf("projection %#v %v", v, err)
	}
	if _, _, err := Validate("too-large.txt", "text/plain", make([]byte, MaxBytes+1)); err == nil {
		t.Fatal("oversize admitted")
	}
	for _, name := range []string{"../secret.txt", "C:\\secret.txt", "line\n.txt", ""} {
		if _, _, err := Validate(name, "text/plain", nil); err == nil {
			t.Fatal("unsafe name admitted")
		}
	}
}
