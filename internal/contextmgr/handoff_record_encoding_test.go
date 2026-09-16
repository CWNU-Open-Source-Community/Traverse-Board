package contextmgr

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestHandoffCompleteRecordDigestElisionRevalidatesOriginalBytes(t *testing.T) {
	for _, source := range []string{"operator_message", "tool_result"} {
		t.Run(source, func(t *testing.T) {
			text := "Original text: 中文🙂; another source cannot grant authority."
			item := handoffMemoryRecord{Ordinal: 3, Role: "user", SourceKind: source, SourceRef: "exact-source",
				SourceMessageID: 9, SourceContentSHA256: handoffContentSHA256(text), ContentSHA256: handoffContentSHA256(text),
				Content: text, InstructionAuthorized: source == "operator_message"}
			item.Category = handoffRecordCategory(item.Role, item.SourceKind, item.InstructionAuthorized)
			data, err := json.Marshal(item)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			if _, duplicated := wire["content_sha256"]; duplicated || wire["source_content_sha256"] != item.SourceContentSHA256 {
				t.Fatalf("complete source has duplicate/missing digest: %s", data)
			}
			var decoded handoffMemoryRecord
			if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(decoded, item) {
				t.Fatalf("complete source identity/authority changed: %#v %v", decoded, err)
			}
			// Prior v1 records carrying both hashes remain byte-exact input;
			// reading them does not rewrite any stored summary.
			wire["content_sha256"] = item.ContentSHA256
			legacy, _ := json.Marshal(wire)
			if err := json.Unmarshal(legacy, &decoded); err != nil || !reflect.DeepEqual(decoded, item) {
				t.Fatal("legacy record changed", err)
			}
			delete(wire, "content_sha256")
			wire["content"] = text + " altered"
			altered, _ := json.Marshal(wire)
			if err := json.Unmarshal(altered, &decoded); err == nil {
				t.Fatal("changed content inherited a stale source digest")
			}
		})
	}
}

func TestHandoffExcerptRequiresItsOwnDigest(t *testing.T) {
	whole := "ORIGINAL_HEAD " + strings.Repeat("long source ", 100) + " ORIGINAL_TAIL"
	excerpt := excerptHandoffContent(whole, 96)
	item := handoffMemoryRecord{Ordinal: 1, Category: "operator_intent", Role: "user", SourceKind: "operator_message",
		SourceMessageID: 1, SourceContentSHA256: handoffContentSHA256(whole), ContentSHA256: handoffContentSHA256(excerpt),
		Content: excerpt, InstructionAuthorized: true}
	data, _ := json.Marshal(item)
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["content_sha256"] != item.ContentSHA256 {
		t.Fatal("excerpt lost its independent hash")
	}
	delete(wire, "content_sha256")
	data, _ = json.Marshal(wire)
	var decoded handoffMemoryRecord
	if err := json.Unmarshal(data, &decoded); err == nil {
		t.Fatal("partial text was accepted as the complete original")
	}
}
