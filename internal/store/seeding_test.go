package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/asdfgh129/ud/internal/core"
)

// The seeding state writes are CONDITIONAL: they must never race the
// scheduler's own transitions (docs/05 §4) or leave a task stuck in Seeding.
func TestUpdateSeedingStatsAndConditionalSeedingState(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	completed := core.TaskID("task-seed-completed")
	pending := core.TaskID("task-seed-pending")
	for id, res := range map[core.TaskID]string{completed: "res-seed-1", pending: "res-seed-2"} {
		if err := st.InsertTask(ctx, &core.Task{
			ID: id, State: core.StatePending, Input: "magnet:?xt=urn:btih:test",
			ResourceID: res, BytesTotal: 100,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE tasks SET state='Completed' WHERE id=?`, string(completed)); err != nil {
		t.Fatal(err)
	}

	if err := st.UpdateSeedingStats(ctx, "res-seed-1", 4096); err != nil {
		t.Fatalf("UpdateSeedingStats: %v", err)
	}
	if err := st.UpdateSeedingStats(ctx, "res-missing", 999); err != nil {
		t.Fatalf("unknown resource must be a no-op, got %v", err)
	}
	got, err := st.GetTask(ctx, completed)
	if err != nil {
		t.Fatal(err)
	}
	if got.BytesUploaded != 4096 {
		t.Fatalf("bytes_uploaded = %d, want 4096", got.BytesUploaded)
	}

	changed, err := st.TrySetSeedingState(ctx, "res-seed-2", true)
	if err != nil || changed {
		t.Fatalf("a task still Pending must never enter Seeding: changed=%v err=%v", changed, err)
	}
	changed, err = st.TrySetSeedingState(ctx, "res-seed-1", true)
	if err != nil || !changed {
		t.Fatalf("Completed -> Seeding must succeed: changed=%v err=%v", changed, err)
	}
	got, _ = st.GetTask(ctx, completed)
	if got.State != core.StateSeeding {
		t.Fatalf("state = %q, want Seeding", got.State)
	}
	changed, err = st.TrySetSeedingState(ctx, "res-seed-1", true)
	if err != nil || changed {
		t.Fatalf("repeated start must be a no-op: changed=%v err=%v", changed, err)
	}

	changed, err = st.TrySetSeedingState(ctx, "res-seed-1", false)
	if err != nil || !changed {
		t.Fatalf("Seeding -> Completed must succeed: changed=%v err=%v", changed, err)
	}
	got, _ = st.GetTask(ctx, completed)
	if got.State != core.StateCompleted || got.BytesUploaded != 4096 {
		t.Fatalf("final state=%q uploaded=%d", got.State, got.BytesUploaded)
	}
	changed, err = st.TrySetSeedingState(ctx, "res-seed-1", false)
	if err != nil || changed {
		t.Fatalf("repeated stop must be a no-op: changed=%v err=%v", changed, err)
	}
}
