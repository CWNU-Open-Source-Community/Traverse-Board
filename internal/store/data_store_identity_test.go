package store

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func TestDataStoreIdentityStableAcrossReopenButIsolatesCopiedAndReplacementDatabases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "original", "cyberagent.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id := st.DataStoreID()
	if !regexp.MustCompile(`^ds1_[0-9a-f]{64}$`).MatchString(id) || strings.Contains(id, path) {
		t.Fatalf("invalid public recovery scope: %q", id)
	}
	if err := st.SetProviderSetting(t.Context(), "route.code", "fixture/model"); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := st.db.QueryRow(`SELECT value || ':' || updated_at FROM provider_setting WHERE key = ?`,
		dataStoreIdentitySeedKey).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var after string
	if err := st.db.QueryRow(`SELECT value || ':' || updated_at FROM provider_setting WHERE key = ?`,
		dataStoreIdentitySeedKey).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if st.DataStoreID() != id || after != before {
		t.Fatal("reopen changed the recovery scope or rewrote its existing seed")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "copied.db")
	if err := os.WriteFile(copyPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	copied, err := Open(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	if copied.DataStoreID() == id {
		t.Fatal("copied database in another Home reused the original recovery scope")
	}
	if route, found, err := copied.GetProviderSetting(t.Context(), "route.code"); err != nil || !found || route != "fixture/model" {
		t.Fatalf("copy changed unrelated settings: found=%t route=%q err=%v", found, route, err)
	}
	var copiedSeed string
	if err := copied.db.QueryRow(`SELECT value || ':' || updated_at FROM provider_setting WHERE key = ?`,
		dataStoreIdentitySeedKey).Scan(&copiedSeed); err != nil || copiedSeed != before {
		t.Fatal("copy initialization rewrote its existing seed")
	}
	// A genuinely new database at the same URL/path must not receive an old
	// unresolved submission merely because the endpoint was reused.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	replacement, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if replacement.DataStoreID() == id || replacement.DataStoreID() == copied.DataStoreID() {
		t.Fatal("replacement database reused an old recovery scope")
	}
}

func TestDataStoreIdentityConcurrentInitializationKeepsOneSeed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cyberagent.db")
	const count = 4
	start := make(chan struct{})
	type result struct {
		id  string
		err error
	}
	results := make(chan result, count)
	var wait sync.WaitGroup
	for range count {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			st, err := Open(path)
			value := result{err: err}
			if err == nil {
				value.id = st.DataStoreID()
				value.err = st.Close()
			}
			results <- value
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	var id string
	for value := range results {
		if value.err != nil {
			t.Fatal(value.err)
		}
		if id == "" {
			id = value.id
		}
		if value.id == "" || value.id != id {
			t.Fatal("concurrent open minted conflicting recovery scopes")
		}
	}
}

func TestDataStoreIdentityRejectsDamagedSeedWithoutOverwritingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cyberagent.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetProviderSetting(t.Context(), dataStoreIdentitySeedKey, "invalid-fixture-seed"); err != nil {
		t.Fatal(err)
	}
	if opened, err := Open(path); err == nil {
		_ = opened.Close()
		t.Fatal("damaged existing recovery seed was silently accepted or replaced")
	}
	value, found, err := st.GetProviderSetting(t.Context(), dataStoreIdentitySeedKey)
	if err != nil || !found || value != "invalid-fixture-seed" {
		t.Fatal("rejected initialization changed the old seed")
	}
}
