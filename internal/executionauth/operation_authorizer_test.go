package executionauth

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/toolcontract"
)

func testOperation() toolcontract.Operation {
	return toolcontract.Operation{ID: "operation-1", Kind: toolcontract.OperationFileWrite,
		ToolID: "write_file", Component: toolcontract.ComponentRef{PackageID: "host", ComponentID: "files"},
		AdapterID: "rooted-files", AdapterRevision: "1", InputFingerprint: strings.Repeat("a", 64),
		Targets: []toolcontract.Target{{Kind: "file", Locator: "workspace:readme.txt"}},
		Effects: []toolcontract.Effect{toolcontract.EffectReversibleWrite}}
}

func TestOperationAuthorizerThreeModesUseVerifiedEffects(t *testing.T) {
	for _, test := range []struct {
		name                        string
		mode                        domain.ExecutionApprovalMode
		effects                     []toolcontract.Effect
		verified, available, active bool
		want                        string
	}{
		{"ask workspace edit", domain.ExecutionApprovalAsk, []toolcontract.Effect{toolcontract.EffectReversibleWrite}, true, true, false, "allow"},
		{"auto workspace read", domain.ExecutionApprovalAuto, []toolcontract.Effect{toolcontract.EffectWorkspaceRead}, true, true, false, "allow"},
		{"ask bounded process", domain.ExecutionApprovalAsk, []toolcontract.Effect{toolcontract.EffectWorkspaceRead, toolcontract.EffectProcess}, true, true, false, "allow"},
		{"process without boundary", domain.ExecutionApprovalAuto, []toolcontract.Effect{toolcontract.EffectProcess}, true, true, false, "deny"},
		{"ask public request", domain.ExecutionApprovalAsk, []toolcontract.Effect{toolcontract.EffectPublicNetwork}, true, true, false, "deny"},
		{"auto public request", domain.ExecutionApprovalAuto, []toolcontract.Effect{toolcontract.EffectPublicNetwork}, true, true, false, "allow"},
		{"annotation is not enforcement", domain.ExecutionApprovalAuto, []toolcontract.Effect{toolcontract.EffectWorkspaceRead}, false, true, false, "deny"},
		{"mixed destructive effects", domain.ExecutionApprovalAuto, []toolcontract.Effect{toolcontract.EffectWorkspaceRead, toolcontract.EffectDestructive}, true, true, false, "deny"},
		{"remote write", domain.ExecutionApprovalAuto, []toolcontract.Effect{toolcontract.EffectRemoteWrite}, true, true, false, "deny"},
		{"secret export", domain.ExecutionApprovalAuto, []toolcontract.Effect{toolcontract.EffectSensitiveExport}, true, true, false, "deny"},
		{"outside", domain.ExecutionApprovalAsk, []toolcontract.Effect{toolcontract.EffectOutside}, true, true, false, "deny"},
		{"unknown", domain.ExecutionApprovalAuto, []toolcontract.Effect{toolcontract.EffectUnknown}, true, true, false, "deny"},
		{"full active unknown", domain.ExecutionApprovalFull, []toolcontract.Effect{toolcontract.EffectUnknown}, false, true, true, "allow"},
		{"full cold", domain.ExecutionApprovalFull, []toolcontract.Effect{toolcontract.EffectWorkspaceRead}, true, true, false, "deny"},
		{"full unavailable", domain.ExecutionApprovalFull, []toolcontract.Effect{toolcontract.EffectUnknown}, false, false, true, "deny"},
	} {
		t.Run(test.name, func(t *testing.T) {
			operation := testOperation()
			operation.Effects = test.effects
			a := NewPolicyAuthorizer(func(context.Context, SubjectRef, toolcontract.Operation, string) (OperationAuthority, error) {
				return OperationAuthority{Mode: test.mode, BindingFingerprint: strings.Repeat("b", 64),
					EffectsVerified: test.verified, RuntimeAvailable: test.available, FullActivated: test.active}, nil
			})
			d, err := a.Authorize(context.Background(), SubjectRef{"run", "actor"}, operation, "")
			if err != nil || d.Outcome != test.want || d.Validate() != nil {
				t.Fatalf("decision=%+v err=%v", d, err)
			}
		})
	}
}

func TestOperationAuthorizerBindsApprovalAndPreservesPendingReview(t *testing.T) {
	op := testOperation()
	fingerprint, _ := toolcontract.FingerprintOperation(op)
	subject := SubjectRef{"run", "actor"}
	for _, mode := range []domain.ExecutionApprovalMode{domain.ExecutionApprovalAsk, domain.ExecutionApprovalAuto, domain.ExecutionApprovalFull} {
		for _, status := range []string{"pending", "approved", "denied"} {
			authority := OperationAuthority{Mode: mode, BindingFingerprint: strings.Repeat("b", 64),
				RuntimeAvailable: true, EffectsVerified: true, FullActivated: true,
				Approval: &BoundApproval{Ref: "approval", Subject: subject, OperationFingerprint: fingerprint, Status: status}}
			a := NewPolicyAuthorizer(func(context.Context, SubjectRef, toolcontract.Operation, string) (OperationAuthority, error) {
				return authority, nil
			})
			d, err := a.Authorize(context.Background(), subject, op, "approval")
			want := map[string]string{"pending": "require_approval", "approved": "allow", "denied": "deny"}[status]
			if err != nil || d.Outcome != want || d.Validate() != nil {
				t.Fatalf("%s/%s decision=%+v err=%v", mode, status, d, err)
			}
			for _, mutate := range []func(*BoundApproval){
				func(p *BoundApproval) { p.Subject.ActorID = "other-actor" },
				func(p *BoundApproval) { p.OperationFingerprint = strings.Repeat("c", 64) },
				func(p *BoundApproval) { p.Ref = "other-approval" },
			} {
				copy := *authority.Approval
				mutate(authority.Approval)
				if _, err := a.Authorize(context.Background(), subject, op, "approval"); err == nil {
					t.Fatal("mismatched approval accepted")
				}
				*authority.Approval = copy
			}
		}
	}
}

func TestOperationAuthorizerDispatchIsOnceAndFreezesInputs(t *testing.T) {
	op := testOperation()
	fingerprint, _ := toolcontract.FingerprintOperation(op)
	a := NewPolicyAuthorizer(func(_ context.Context, _ SubjectRef, actual toolcontract.Operation, _ string) (OperationAuthority, error) {
		if actual.Targets[0].Locator != "workspace:readme.txt" {
			t.Error("captured operation was mutated")
		}
		// A resolver cannot mutate the retained decision's operation either.
		actual.Effects[0] = toolcontract.EffectUnknown
		return OperationAuthority{Mode: domain.ExecutionApprovalAsk, BindingFingerprint: strings.Repeat("b", 64),
			RuntimeAvailable: true, EffectsVerified: true}, nil
	})
	d, err := a.Authorize(context.Background(), SubjectRef{"run", "actor"}, op, "")
	if err != nil {
		t.Fatal(err)
	}
	op.Targets[0].Locator = "outside:secret"
	op.Effects[0] = toolcontract.EffectUnknown
	var allowed atomic.Int32
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			if d.BeforeDispatch(context.Background(), fingerprint) == nil {
				allowed.Add(1)
			}
		})
	}
	group.Wait()
	if allowed.Load() != 1 {
		t.Fatalf("dispatches=%d", allowed.Load())
	}
}

func TestOperationAuthorizerRechecksRevocationCancellationAndDrift(t *testing.T) {
	for _, test := range []string{"revoked", "cancelled", "changed-input", "changed-authority", "cancel-during-resolve"} {
		t.Run(test, func(t *testing.T) {
			op := testOperation()
			fingerprint, _ := toolcontract.FingerprintOperation(op)
			authority := OperationAuthority{Mode: domain.ExecutionApprovalAsk, BindingFingerprint: strings.Repeat("b", 64), RuntimeAvailable: true, EffectsVerified: true}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			resolveCalls := 0
			a := NewPolicyAuthorizer(func(context.Context, SubjectRef, toolcontract.Operation, string) (OperationAuthority, error) {
				resolveCalls++
				if test == "cancel-during-resolve" && resolveCalls == 2 {
					cancel()
				}
				return authority, nil
			})
			d, err := a.Authorize(ctx, SubjectRef{"run", "actor"}, op, "")
			if err != nil {
				t.Fatal(err)
			}
			switch test {
			case "revoked":
				authority.RuntimeAvailable = false
			case "cancelled":
				cancel()
			case "changed-input":
				fingerprint = strings.Repeat("c", 64)
			case "changed-authority":
				authority.BindingFingerprint = strings.Repeat("d", 64)
			}
			if err := d.BeforeDispatch(ctx, fingerprint); err == nil {
				t.Fatal("stale dispatch accepted")
			}
		})
	}
}

func TestOperationAuthorizerLaterMutationCannotReissueAuthority(t *testing.T) {
	op := testOperation()
	fingerprint, _ := toolcontract.FingerprintOperation(op)
	subject := SubjectRef{"run", "actor"}
	authority := OperationAuthority{Mode: domain.ExecutionApprovalAsk, BindingFingerprint: strings.Repeat("b", 64), RuntimeAvailable: true, EffectsVerified: true}
	a := NewPolicyAuthorizer(func(context.Context, SubjectRef, toolcontract.Operation, string) (OperationAuthority, error) {
		return authority, nil
	})
	d, err := a.Authorize(context.Background(), subject, op, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.BeforeDispatch(context.Background(), fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := a.Recheck(context.Background(), subject, op, "", d.AuthorizationRef); err != nil {
		t.Fatal(err)
	}
	authority.BindingFingerprint = strings.Repeat("c", 64)
	if err := a.Recheck(context.Background(), subject, op, "", d.AuthorizationRef); err == nil {
		t.Fatal("later mutation acquired changed authority")
	}
}
