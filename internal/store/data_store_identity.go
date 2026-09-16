package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const dataStoreIdentitySeedKey = "recovery.data_store_seed.v1"

// DataStoreID is a non-secret recovery namespace. It is initialized during Open,
// so an HTTP health read never creates or updates persisted settings.
func (s *SQLiteStore) DataStoreID() string {
	if s == nil {
		return ""
	}
	return s.dataStoreID
}

func (s *SQLiteStore) initializeDataStoreIdentity(ctx context.Context, databasePath string) error {
	canonical, err := filepath.EvalSymlinks(databasePath)
	if err != nil {
		return errors.New("resolve data store recovery namespace")
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return errors.New("resolve absolute data store recovery namespace")
	}
	canonical = filepath.Clean(canonical)
	if runtime.GOOS == "windows" {
		canonical = strings.ToLower(canonical)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var seed string
	err = tx.QueryRowContext(ctx, `SELECT value FROM provider_setting WHERE key = ?`,
		dataStoreIdentitySeedKey).Scan(&seed)
	if errors.Is(err, sql.ErrNoRows) {
		var entropy [32]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return errors.New("initialize data store recovery namespace")
		}
		seed = hex.EncodeToString(entropy[:])
		if _, err := tx.ExecContext(ctx, `INSERT INTO provider_setting (key, value, updated_at)
			VALUES (?, ?, ?)`, dataStoreIdentitySeedKey, seed, ts(time.Now().UTC())); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	decoded, err := hex.DecodeString(seed)
	if err != nil || len(decoded) != 32 || seed != strings.ToLower(seed) {
		// Never silently mint another identity for existing, damaged metadata:
		// an unresolved request may still belong to that exact data store.
		return errors.New("stored data store recovery namespace is invalid")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// The seed distinguishes replacement databases at one address. The physical
	// path distinguishes a copied database opened in another Home. Neither the
	// seed nor the private path is exposed in the public recovery namespace.
	digest := sha256.Sum256([]byte("data_store_scope.v1\x00" + seed + "\x00" + canonical))
	s.dataStoreID = "ds1_" + hex.EncodeToString(digest[:])
	return nil
}
