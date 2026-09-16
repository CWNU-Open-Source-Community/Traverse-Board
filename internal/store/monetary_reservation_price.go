package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"cyberagent-workbench/internal/pricing"
)

// ModelReservationPrice reads the immutable price accepted at reserve time.
// Rotation or expiry of the active table cannot change an in-flight bill.
func (s *SQLiteStore) ModelReservationPrice(ctx context.Context, runID, scope string, attemptNumber int64) (pricing.Entry, bool, error) {
	return modelReservationPriceTx(ctx, s.db, runID, scope, attemptNumber)
}

func modelReservationPriceTx(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, runID, scope string, attemptNumber int64) (pricing.Entry, bool, error) {
	var encoded, provider, model string
	err := q.QueryRowContext(ctx, `SELECT prices.entries_json,reserved.provider,reserved.model
		FROM run_monetary_reservations reserved JOIN provider_price_snapshots prices
		ON prices.fingerprint=reserved.price_fingerprint
		WHERE reserved.run_id=? AND reserved.scope=? AND reserved.attempt_number=?`, runID, scope, attemptNumber).Scan(&encoded, &provider, &model)
	if errors.Is(err, sql.ErrNoRows) {
		return pricing.Entry{}, false, nil
	}
	if err != nil {
		return pricing.Entry{}, false, err
	}
	var entries []pricing.Entry
	if err := json.Unmarshal([]byte(encoded), &entries); err != nil {
		return pricing.Entry{}, false, errors.New("reserved price snapshot entries are malformed")
	}
	for _, entry := range entries {
		if entry.Provider == provider && entry.Model == model {
			return entry, true, nil
		}
	}
	return pricing.Entry{}, false, nil
}
