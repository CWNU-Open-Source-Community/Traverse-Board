package executionauth

import (
	"context"
	"encoding/json"
	"testing"
)

func TestOperationDecisionCannotSerializeExecutableAuthority(t *testing.T) {
	d := Decision{Outcome: "allow", ReasonCode: "bounded_workspace", AuthorizationRef: "auth-ref",
		BeforeDispatch: func(context.Context, string) error { return nil }}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var restored Decision
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Validate() == nil {
		t.Fatal("serialized reference restored executable authority")
	}
	for _, invalid := range []Decision{
		{Outcome: "allow", ReasonCode: "reason", AuthorizationRef: "auth-ref"},
		{Outcome: "require_approval", ReasonCode: "reason", ApprovalRef: "pending", BeforeDispatch: d.BeforeDispatch},
		{Outcome: "deny", ReasonCode: "reason", AuthorizationRef: "auth-ref"},
		{Outcome: "allow", ReasonCode: "reason", AuthorizationRef: "bad\nref", BeforeDispatch: d.BeforeDispatch},
	} {
		if invalid.Validate() == nil {
			t.Fatal("invalid decision carried executable authority")
		}
	}
	for _, valid := range []Decision{
		{Outcome: "require_approval", ReasonCode: "exact_approval", ApprovalRef: "pending"},
		{Outcome: "deny", ReasonCode: "unavailable"},
	} {
		if err := valid.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}
