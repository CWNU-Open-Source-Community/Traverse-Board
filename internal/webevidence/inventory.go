package webevidence

import (
	"context"
	"errors"
	"strings"
	"time"
)

const MaxInventoryItems = 500

type InventoryStore interface {
	ListWebSources(context.Context, string, int) ([]Source, error)
	ListWebSnapshots(context.Context, string, int) ([]Snapshot, error)
	ListWebCitations(context.Context, string, int) ([]Citation, error)
}

func LoadInventory(ctx context.Context, store InventoryStore, runID string, limit int,
	at time.Time,
) (Inventory, error) {
	runID = strings.TrimSpace(runID)
	if store == nil || runID == "" || limit < 1 || limit > MaxInventoryItems || at.IsZero() {
		return Inventory{}, errors.New("web evidence inventory input is invalid")
	}
	sources, err := store.ListWebSources(ctx, runID, limit)
	if err != nil {
		return Inventory{}, err
	}
	snapshots, err := store.ListWebSnapshots(ctx, runID, limit)
	if err != nil {
		return Inventory{}, err
	}
	citations, err := store.ListWebCitations(ctx, runID, limit)
	if err != nil {
		return Inventory{}, err
	}
	if len(sources) > limit || len(snapshots) > limit || len(citations) > limit {
		return Inventory{}, errors.New("web evidence inventory store exceeded its requested bound")
	}
	for _, source := range sources {
		if source.Validate() != nil || source.RunID != runID {
			return Inventory{}, errors.New("web evidence inventory contains an invalid source")
		}
	}
	for _, snapshot := range snapshots {
		if snapshot.Validate() != nil || snapshot.RunID != runID {
			return Inventory{}, errors.New("web evidence inventory contains an invalid snapshot")
		}
	}
	for _, citation := range citations {
		if citation.Validate() != nil || citation.RunID != runID {
			return Inventory{}, errors.New("web evidence inventory contains an invalid citation")
		}
	}
	inventory := Inventory{ProtocolVersion: ProtocolVersion, RunID: runID,
		Sources:   make([]SourcePresentation, len(sources)),
		Snapshots: make([]SnapshotPresentation, len(snapshots)),
		Citations: make([]CitationPresentation, len(citations)),
		Untrusted: true}
	for index, source := range sources {
		inventory.Sources[index] = PresentSource(source)
	}
	for index, snapshot := range snapshots {
		inventory.Snapshots[index] = PresentSnapshot(snapshot, at)
	}
	for index, citation := range citations {
		inventory.Citations[index] = PresentCitation(citation, at)
	}
	return inventory, nil
}
