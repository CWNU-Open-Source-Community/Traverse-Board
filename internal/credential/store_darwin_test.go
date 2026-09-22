//go:build darwin && cgo

package credential

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDarwinStoreValidatesBeforeKeychainAccess(t *testing.T) {
	// This inaccessible target must never be opened by rejected requests.
	s := darwinStore{keychainPath: "/nonexistent/traverse-validation-test.keychain"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Put(ctx, "openai", "test-key-value"); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled put did not preserve context cancellation")
	}
	if err := s.Delete(ctx, "openai"); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled delete did not preserve context cancellation")
	}
	if _, found, err := s.Get(ctx, "openai"); found || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled get did not preserve context cancellation")
	}
	if found, err := s.Configured(ctx, "openai"); found || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled status did not preserve context cancellation")
	}
	ctx = context.Background()
	for _, name := range []string{"", " openai", "openai/other", "embedded\x00name", strings.Repeat("a", 65)} {
		if err := s.Put(ctx, name, "test-key-value"); err == nil || err.Error() != "credential name or secret is invalid" {
			t.Fatal("invalid put name reached Keychain access")
		}
		if err := s.Delete(ctx, name); err == nil || err.Error() != "credential name is invalid" {
			t.Fatal("invalid delete name reached Keychain access")
		}
		if _, found, err := s.Get(ctx, name); found || err == nil || err.Error() != "credential name is invalid" {
			t.Fatal("invalid get name reached Keychain access")
		}
	}
	for _, secret := range []string{"", " leading", "line\nbreak", string([]byte{0xff}), strings.Repeat("a", MaxSecretBytes+1)} {
		if err := s.Put(ctx, "openai", secret); err == nil || err.Error() != "credential name or secret is invalid" {
			t.Fatal("invalid secret reached Keychain access")
		}
	}
}

func TestDarwinUpsertDoesNotRetryAuthorizationFailures(t *testing.T) {
	for _, status := range []int32{darwinUserCanceled, darwinAuthFailed, darwinInteraction, darwinInteractionNeed, -9999} {
		updates, adds := 0, 0
		err := darwinUpsert(context.Background(), func() int32 {
			updates++
			return status
		}, func() int32 {
			adds++
			return darwinSuccess
		})
		var keychainErr darwinKeychainError
		if !errors.As(err, &keychainErr) || keychainErr.status != status || updates != 1 || adds != 0 {
			t.Fatal("authorization/storage failure was swallowed or retried")
		}
	}
}

func TestDarwinUpsertHandlesDuplicateWithOneRetry(t *testing.T) {
	for _, final := range []int32{darwinSuccess, darwinItemNotFound, darwinDuplicateItem, darwinUserCanceled} {
		updates, adds := 0, 0
		err := darwinUpsert(context.Background(), func() int32 {
			updates++
			if updates == 1 {
				return darwinItemNotFound
			}
			return final
		}, func() int32 {
			adds++
			return darwinDuplicateItem
		})
		if updates != 2 || adds != 1 || (err == nil) != (final == darwinSuccess) {
			t.Fatal("duplicate retry was not bounded or lost its result")
		}
	}
}

func TestDarwinUpsertMissingItemAddsOnce(t *testing.T) {
	for _, result := range []int32{darwinSuccess, darwinUserCanceled} {
		updates, adds := 0, 0
		err := darwinUpsert(context.Background(), func() int32 {
			updates++
			return darwinItemNotFound
		}, func() int32 {
			adds++
			return result
		})
		if updates != 1 || adds != 1 || (err == nil) != (result == darwinSuccess) {
			t.Fatal("missing item add did not preserve its result")
		}
	}
}

func TestDarwinUpsertCancellationDoesNotLeaveBackgroundWrites(t *testing.T) {
	for _, cancelAfter := range []string{"before", "update", "add", "success"} {
		t.Run(cancelAfter, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			updates, adds := 0, 0
			if cancelAfter == "before" {
				cancel()
			}
			err := darwinUpsert(ctx, func() int32 {
				updates++
				if cancelAfter == "success" {
					cancel()
					return darwinSuccess
				}
				if cancelAfter == "update" {
					cancel()
				}
				return darwinItemNotFound
			}, func() int32 {
				adds++
				cancel()
				return darwinDuplicateItem
			})
			if cancelAfter == "success" {
				if err != nil || updates != 1 || adds != 0 {
					t.Fatal("completed OS success was incorrectly reported as canceled")
				}
				return
			}
			if !errors.Is(err, context.Canceled) || updates > 1 || adds > 1 {
				t.Fatal("canceled operation continued its mutation sequence")
			}
			if cancelAfter == "before" && updates+adds != 0 || cancelAfter == "update" && adds != 0 {
				t.Fatal("cancellation did not prevent the next OS call")
			}
		})
	}
}

func TestDarwinStatusErrorsDoNotCollapseCanceledOrLockedToMissing(t *testing.T) {
	if darwinStatusError(darwinSuccess) != nil {
		t.Fatal("success became an error")
	}
	for _, status := range []int32{darwinUserCanceled, darwinAuthFailed, darwinInteraction, darwinInteractionNeed, -9999} {
		err := darwinStatusError(status)
		var typed darwinKeychainError
		if !errors.As(err, &typed) || typed.status != status || typed.status == darwinItemNotFound {
			t.Fatal("Keychain status lost its identity")
		}
		if !strings.HasPrefix(err.Error(), "macOS Keychain ") || !strings.Contains(err.Error(), "OSStatus ") {
			t.Fatal("Keychain error is missing its fixed safe diagnostic")
		}
	}
}
