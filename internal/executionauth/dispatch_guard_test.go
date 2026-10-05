package executionauth

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/toolcontract"
)

func TestRecheckingDispatchGuardUsesInitialDecisionThenLiveRechecks(t *testing.T) {
	op := testOperation()
	fingerprint, _ := toolcontract.FingerprintOperation(op)
	subject := SubjectRef{"run", "actor"}
	authority := OperationAuthority{Mode: domain.ExecutionApprovalAsk, BindingFingerprint: strings.Repeat("b", 64),
		RuntimeAvailable: true, EffectsVerified: true}
	resolveCalls := 0
	authorizer := NewPolicyAuthorizer(func(context.Context, SubjectRef, toolcontract.Operation, string) (OperationAuthority, error) {
		resolveCalls++
		return authority, nil
	})
	decision, err := authorizer.Authorize(context.Background(), subject, op, "")
	if err != nil {
		t.Fatal(err)
	}
	guard := NewRecheckingDispatchGuard(fingerprint, decision.BeforeDispatch, func(ctx context.Context) error {
		return authorizer.Recheck(ctx, subject, op, "", decision.AuthorizationRef)
	}, "native dispatch locked")
	for range 3 {
		if err := guard(context.Background(), fingerprint); err != nil {
			t.Fatal(err)
		}
	}
	if resolveCalls != 4 {
		t.Fatalf("authority resolutions=%d, want authorize plus three live checks", resolveCalls)
	}
	authority.BindingFingerprint = strings.Repeat("c", 64)
	if err := guard(context.Background(), fingerprint); err == nil {
		t.Fatal("later step renewed changed authority")
	}
	authority.BindingFingerprint = strings.Repeat("b", 64)
	if err := guard(context.Background(), fingerprint); err == nil || err.Error() != "native dispatch locked" {
		t.Fatalf("denied guard revived after authority returned: %v", err)
	}
	if resolveCalls != 5 {
		t.Fatalf("locked guard resolved authority again: %d", resolveCalls)
	}
}

func TestRecheckingDispatchGuardInputChangeLocksBeforeOrAfterFirstDispatch(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_first_dispatch", true: "after_first_dispatch"}[started], func(t *testing.T) {
			checks := 0
			guard := NewRecheckingDispatchGuard("expected", func(context.Context, string) error {
				checks++
				return nil
			}, func(context.Context) error {
				checks++
				return nil
			}, "inputs changed")
			if started {
				if err := guard(context.Background(), "expected"); err != nil {
					t.Fatal(err)
				}
			}
			before := checks
			for _, actual := range []string{"changed", "expected"} {
				if err := guard(context.Background(), actual); err == nil || err.Error() != "inputs changed" {
					t.Fatalf("fingerprint %q accepted or lost native denial: %v", actual, err)
				}
			}
			if checks != before {
				t.Fatal("input rejection or retry invoked authority checks")
			}
		})
	}
}

func TestRecheckingDispatchGuardKeepsFirstAndLaterCheckFailures(t *testing.T) {
	for _, failFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "first_dispatch", false: "later_recheck"}[failFirst], func(t *testing.T) {
			failure := errors.New("native authority revoked")
			checks := 0
			guard := NewRecheckingDispatchGuard("expected", func(context.Context, string) error {
				checks++
				if failFirst {
					return failure
				}
				return nil
			}, func(context.Context) error {
				checks++
				return failure
			}, "dispatch locked")
			if !failFirst {
				if err := guard(context.Background(), "expected"); err != nil {
					t.Fatal(err)
				}
			}
			if err := guard(context.Background(), "expected"); !errors.Is(err, failure) {
				t.Fatalf("native denial was replaced: %v", err)
			}
			before := checks
			if err := guard(context.Background(), "expected"); err == nil || err.Error() != "dispatch locked" {
				t.Fatalf("denied guard allowed retry: %v", err)
			}
			if checks != before {
				t.Fatal("denied guard re-entered authority checks")
			}
		})
	}
}

func TestRecheckingDispatchGuardSerializesConcurrentChecks(t *testing.T) {
	var active, firstCalls, rechecks atomic.Int32
	check := func() error {
		if active.Add(1) != 1 {
			t.Error("authority checks overlapped")
		}
		defer active.Add(-1)
		runtime.Gosched()
		return nil
	}
	guard := NewRecheckingDispatchGuard("expected", func(context.Context, string) error {
		firstCalls.Add(1)
		return check()
	}, func(context.Context) error {
		rechecks.Add(1)
		return check()
	}, "dispatch locked")
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			if err := guard(context.Background(), "expected"); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if firstCalls.Load() != 1 || rechecks.Load() != 15 {
		t.Fatalf("initial dispatches=%d rechecks=%d", firstCalls.Load(), rechecks.Load())
	}
}
