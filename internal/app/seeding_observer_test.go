package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/asdfgh129/ud/internal/core"
	"github.com/asdfgh129/ud/internal/store"
	bttransport "github.com/asdfgh129/ud/internal/transport/bt"
)

func TestSeedingObserverPersistsCountersAndDrivesState(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	id := core.TaskID("task-observer-1")
	if err := st.InsertTask(ctx, &core.Task{
		ID: id, State: core.StatePending, Input: "magnet:?xt=urn:btih:test",
		ResourceID: "res-observer-1", BytesTotal: 100,
	}); err != nil {
		t.Fatal(err)
	}
	// scheduler.Commit is the last DB write of a download; the observer must
	// only flip the state after that point.
	if err := st.Commit(ctx, id, "res-observer-1", 0, "out.bin"); err != nil {
		t.Fatal(err)
	}

	o := newSeedingObserver(st, nil)
	o.SeedingStarted(ctx, "res-observer-1")
	o.SeedingProgress(ctx, bttransport.SeedingStats{
		ResourceID: "res-observer-1", Uploaded: 2048, Downloaded: 100, Ratio: 20.48,
	})
	got, err := st.GetTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.BytesUploaded != 2048 || got.State != core.StateSeeding {
		t.Fatalf("after start: state=%q uploaded=%d", got.State, got.BytesUploaded)
	}

	o.SeedingProgress(ctx, bttransport.SeedingStats{
		ResourceID: "res-observer-1", Uploaded: 4096, Downloaded: 100,
	})
	got, _ = st.GetTask(ctx, id)
	if got.BytesUploaded != 4096 || got.State != core.StateSeeding {
		t.Fatalf("after progress: state=%q uploaded=%d", got.State, got.BytesUploaded)
	}

	o.SeedingStopped(ctx, "res-observer-1", "seed-ratio")
	got, _ = st.GetTask(ctx, id)
	if got.State != core.StateCompleted || got.BytesUploaded != 4096 {
		t.Fatalf("after stop: state=%q uploaded=%d", got.State, got.BytesUploaded)
	}
}

func TestSeedingObserverWithoutStoreIsSafe(t *testing.T) {
	o := newSeedingObserver(nil, nil)
	ctx := context.Background()
	o.SeedingStarted(ctx, "res-nil")
	o.SeedingProgress(ctx, bttransport.SeedingStats{ResourceID: "res-nil", Uploaded: 1})
	o.SeedingStopped(ctx, "res-nil", "context-canceled")
}
