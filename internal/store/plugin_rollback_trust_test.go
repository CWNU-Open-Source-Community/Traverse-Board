package store

import (
	"context"
	"crypto/ed25519"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/plugins"
)

// Interleave a real committed publisher update after the Service's trust read
// and before the real rollback transaction, without timing-dependent sleeps.
type rollbackPublisherRaceStore struct {
	*SQLiteStore
	beforeSwitch func(context.Context) error
}

func (s *rollbackPublisherRaceStore) RollbackPluginInstallation(ctx context.Context,
	current plugins.Installation, currentExpected int64,
	target plugins.Installation, targetExpected int64, authority plugins.PublisherAuthority,
) (plugins.Installation, plugins.Installation, error) {
	if callback := s.beforeSwitch; callback != nil {
		s.beforeSwitch = nil
		if err := callback(ctx); err != nil {
			return plugins.Installation{}, plugins.Installation{}, err
		}
	}
	return s.SQLiteStore.RollbackPluginInstallation(ctx, current, currentExpected, target, targetExpected, authority)
}

func TestPluginRollbackPublisherTrustIsBoundInsideTransaction(t *testing.T) {
	for _, state := range []string{"revoked", "revoked-alias-name", "revoked-and-retrusted", "new-trust-record"} {
		t.Run(state, func(t *testing.T) {
			ctx := t.Context()
			st, err := Open(filepath.Join(t.TempDir(), "rollback-publisher-race.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			service, err := plugins.NewService(st)
			if err != nil {
				t.Fatal(err)
			}
			_, aliceKey, err := ed25519.GenerateKey(nil)
			if err != nil {
				t.Fatal(err)
			}
			_, bobKey, err := ed25519.GenerateKey(nil)
			if err != nil {
				t.Fatal(err)
			}
			alice := stageHookPluginFixture(t, ctx, service, "publisher-race", "1.0.0", "", aliceKey)
			trustInstallationID := alice.ID
			var trust plugins.PublisherTrust
			if state != "new-trust-record" {
				trust, err = service.TrustPublisher(ctx, alice.ID, "Alice reviewer")
				if err != nil {
					t.Fatal(err)
				}
			}
			if state == "revoked-alias-name" {
				alice = stageHookPluginFixture(t, ctx, service, "publisher-race", "1.1.0", alice.ID, aliceKey, "renamed.publisher")
			}
			alice = reviewHookPluginFixture(t, ctx, service, alice, plugins.ReviewApprove, true)
			alice = reviewHookPluginFixture(t, ctx, service, alice, plugins.ReviewEnable, true)
			bob := stageHookPluginFixture(t, ctx, service, "publisher-race", "2.0.0", alice.ID, bobKey)
			if _, err := service.TrustPublisher(ctx, bob.ID, "Bob reviewer"); err != nil {
				t.Fatal(err)
			}
			bob = reviewHookPluginFixture(t, ctx, service, bob, plugins.ReviewApprove, false)
			bob = reviewHookPluginFixture(t, ctx, service, bob, plugins.ReviewEnable, false)
			alice, err = st.GetPluginInstallation(ctx, alice.ID)
			if err != nil || alice.State != plugins.StateRolledBack || alice.PublisherFingerprint == bob.PublisherFingerprint {
				t.Fatalf("race precondition Alice=%#v Bob=%#v err=%v", alice, bob, err)
			}
			var transitionsBefore int
			if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installation_transitions WHERE installation_id IN (?, ?)`, alice.ID, bob.ID).Scan(&transitionsBefore); err != nil {
				t.Fatal(err)
			}
			racingStore := &rollbackPublisherRaceStore{SQLiteStore: st, beforeSwitch: func(ctx context.Context) error {
				if state != "new-trust-record" {
					if _, err := service.RevokePublisher(ctx, trust.Fingerprint, trust.Generation, "Alice revoker"); err != nil {
						return err
					}
				}
				if state != "revoked" && state != "revoked-alias-name" {
					_, err := service.TrustPublisher(ctx, alice.ID, "Alice latest reviewer")
					return err
				}
				return nil
			}}
			racingService, err := plugins.NewService(racingStore)
			if err != nil {
				t.Fatal(err)
			}
			request := plugins.RollbackRequest{ExpectedCurrentFingerprint: bob.PackageFingerprint,
				ExpectedCurrentGeneration: bob.Generation, ExpectedTargetFingerprint: alice.PackageFingerprint,
				ExpectedTargetGeneration: alice.Generation, Capabilities: []plugins.Capability{plugins.CapabilityHooks},
				ConfirmUntrusted: true, ReviewedBy: "rollback reviewer"}
			_, _, err = racingService.Rollback(ctx, bob.ID, alice.ID, request)
			wantCode := apperror.CodeConflict
			if state == "revoked" || state == "revoked-alias-name" {
				wantCode = apperror.CodePolicyDenied
			}
			if apperror.CodeOf(apperror.Normalize(err)) != wantCode || racingStore.beforeSwitch != nil {
				t.Fatalf("interleaved publisher update accepted: err=%v", err)
			}
			for _, previous := range []plugins.Installation{alice, bob} {
				stored, err := st.GetPluginInstallation(ctx, previous.ID)
				if err != nil || stored.Generation != previous.Generation || stored.State != previous.State {
					t.Fatalf("rejected rollback changed installation=%#v err=%v", stored, err)
				}
			}
			var transitionsAfter int
			if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installation_transitions WHERE installation_id IN (?, ?)`, alice.ID, bob.ID).Scan(&transitionsAfter); err != nil || transitionsAfter != transitionsBefore {
				t.Fatalf("rejected rollback added transition: before=%d after=%d err=%v", transitionsBefore, transitionsAfter, err)
			}
			active, err := service.ActiveHooks(ctx)
			if err != nil || len(active) != 1 || active[0].PluginFingerprint != bob.PackageFingerprint {
				t.Fatalf("rejected rollback changed active Hooks=%#v err=%v", active, err)
			}
			if state == "revoked-alias-name" {
				history, err := service.History(ctx, alice.ID)
				if err != nil || history.Publisher == nil || history.Publisher.State != plugins.PublisherRevoked || history.Publisher.Fingerprint != alice.PublisherFingerprint {
					t.Fatalf("alias history hid actual key revocation: history=%#v err=%v", history, err)
				}
				// The same public key remains revoked under another display name,
				// including ordinary review and explicit untrusted confirmation.
				next := stageHookPluginFixture(t, ctx, service, "publisher-race", "3.0.0", "", aliceKey, "renamed.publisher")
				if _, err := service.Review(ctx, next.ID, plugins.ReviewRequest{Action: plugins.ReviewApprove,
					ExpectedPackageFingerprint: next.PackageFingerprint, ExpectedGeneration: next.Generation,
					ConfirmUntrusted: true, ReviewedBy: "alias reviewer"}); apperror.CodeOf(apperror.Normalize(err)) != apperror.CodePolicyDenied {
					t.Fatalf("renamed revoked signing key bypassed ordinary review: %v", err)
				}
				if _, _, err := service.Rollback(ctx, bob.ID, alice.ID, request); apperror.CodeOf(apperror.Normalize(err)) != apperror.CodePolicyDenied {
					t.Fatalf("renamed revoked signing key bypassed Service trust gate: %v", err)
				}
				if _, err := service.TrustPublisher(ctx, trustInstallationID, "explicit key retrust reviewer"); err != nil {
					t.Fatal(err)
				}
				if _, restored, err := service.Rollback(ctx, bob.ID, alice.ID, request); err != nil || restored.State != plugins.StateEnabled {
					t.Fatalf("explicit key retrust did not restore reviewed alias: restored=%#v err=%v", restored, err)
				}
			} else if state != "revoked" {
				if _, restored, err := service.Rollback(ctx, bob.ID, alice.ID, request); err != nil || restored.State != plugins.StateEnabled {
					t.Fatalf("fresh trust read did not allow rollback: restored=%#v err=%v", restored, err)
				}
			}
		})
	}
}
