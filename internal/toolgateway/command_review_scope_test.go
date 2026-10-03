package toolgateway

import (
	"encoding/json"
	"testing"
)

func TestCommandReviewScopeIsMetadataOnlyAndSingleCommand(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal(commandRuntimeValidPayload("Write-Output ok"), &payload); err != nil {
		t.Fatal(err)
	}
	scope := map[string]any{"risk_kinds": []string{"other_high_risk"}, "other_risk_reason": "local verification"}
	payload["review_scope"] = scope
	raw, _ := json.Marshal(payload)
	input, canonical, err := NormalizeCommandRuntimePayload(raw)
	if err != nil || input.ReviewScope == nil {
		t.Fatal(string(canonical), err)
	}
	for _, authority := range []string{"lease_id", "grant_id", "permission_mode", "runtime_epoch", "run_authorization_fence", "grant_max_uses"} {
		scope[authority] = "model-supplied"
		raw, _ = json.Marshal(payload)
		if _, _, err := NormalizeCommandRuntimePayload(raw); err == nil {
			t.Fatalf("scope accepted authority %s", authority)
		}
		delete(scope, authority)
	}
	payload["review_scope"] = nil
	raw, _ = json.Marshal(payload)
	if _, _, err := NormalizeCommandRuntimePayload(raw); err == nil {
		t.Fatal("null review scope accepted")
	}
	payload["review_scope"] = scope
	commands := payload["commands"].([]any)
	payload["commands"] = append(commands, commands[0])
	raw, _ = json.Marshal(payload)
	if _, _, err := NormalizeCommandRuntimePayload(raw); err == nil {
		t.Fatal("bounded review accepted more than one exact command")
	}
	payload = map[string]any{"version": CommandRuntimeToolProtocolVersion, "action": "list", "review_scope": scope}
	raw, _ = json.Marshal(payload)
	if _, _, err := NormalizeCommandRuntimePayload(raw); err == nil {
		t.Fatal("read-only action accepted process scope")
	}
	var schema map[string]any
	if err := json.Unmarshal(commandRuntimeDefinition.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	properties := schema["properties"].(map[string]any)
	if _, ok := properties["review_scope"]; !ok {
		t.Fatal("public schema omitted review scope")
	}
}
