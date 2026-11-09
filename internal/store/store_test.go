package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/asdfgh129/ud/internal/core"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "state.db"), core.NewEventBus(64))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestSchemaAndIdempotentMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	for i := 0; i < 2; i++ {
		st, err := Open(path, nil)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		v, err := st.SchemaVersion(context.Background())
		if err != nil || v != 1 {
			t.Fatalf("schema_version=%d err=%v", v, err)
		}
		st.Close()
	}
}

func TestTaskOptionsAndStateTransitions(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	task := &core.Task{
		ID: core.NewID(), State: core.StatePending, Input: "https://x/a.bin",
		InputKind: core.InputURL, CreatedAt: now, UpdatedAt: now,
		Options: core.TaskOptions{Dir: "/tmp/dl", MaxConnections: 8, ChunkSize: 1 << 20,
			Headers: map[string]string{"Referer": "https://x"}},
	}
	if err := st.InsertTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetTask(ctx, task.ID)
	if err != nil || !reflect.DeepEqual(got.Options, task.Options) {
		t.Fatalf("options round trip: %+v err=%v", got.Options, err)
	}
	if err := st.UpdateState(ctx, task.ID, StateUpdate{To: core.StateResolving}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateState(ctx, task.ID, StateUpdate{To: core.StateCompleted}); err == nil {
		t.Fatal("Resolving -> Completed must be rejected")
	}
	total := int64(1024)
	if err := st.UpdateState(ctx, task.ID, StateUpdate{To: core.StateReady, BytesTotal: &total}); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetTask(ctx, task.ID)
	if got.BytesTotal != total {
		t.Fatalf("bytesTotal=%d", got.BytesTotal)
	}
	// PATCH must not erase the runtime temp_path option.
	_ = st.SetOption(ctx, task.ID, "temp_path", "/tmp/dl/a.bin.part")
	opt := got.Options
	opt.MaxConnections = 16
	if err := st.SaveOptions(ctx, task.ID, opt); err != nil {
		t.Fatal(err)
	}
	v, _ := st.Option(ctx, task.ID, "temp_path")
	if v != "/tmp/dl/a.bin.part" {
		t.Fatalf("temp_path lost: %q", v)
	}
}

// An empty table makes every SUM() return NULL; /stats must still answer 0.
func TestTaskSummaryOnEmptyDatabase(t *testing.T) {
	st := newTestStore(t)
	total, running, completed, failed, bytesDone, err := st.TaskSummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || running != 0 || completed != 0 || failed != 0 || bytesDone != 0 {
		t.Fatalf("summary=%d/%d/%d/%d/%d", total, running, completed, failed, bytesDone)
	}
}

// docs/06 §6: the DB may hold only cred_ref, never a plaintext credential.
func TestSensitiveHeadersNeverPersisted(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	id := core.NewID()
	const secret = "Bearer SUPER-SECRET-9f3a"
	task := &core.Task{
		ID: id, State: core.StatePending, Input: "https://x/a.bin",
		InputKind: core.InputURL, CreatedAt: now, UpdatedAt: now,
		Options: core.TaskOptions{
			CredentialRef: "mem://ud/cred/1",
			Headers: map[string]string{
				"Referer": "https://x", "Authorization": secret,
				"Cookie": "sid=" + secret, "X-Api-Key": secret,
			},
		},
	}
	if err := st.InsertTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	raw, err := st.Option(ctx, id, "options")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "SUPER-SECRET") {
		t.Fatalf("credential leaked into task_options: %s", raw)
	}
	got, err := st.GetTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Options.Headers) != 1 || got.Options.Headers["Referer"] != "https://x" {
		t.Fatalf("non-sensitive headers lost: %+v", got.Options.Headers)
	}
	if got.Options.CredentialRef != "mem://ud/cred/1" {
		t.Fatalf("credentialRef lost: %q", got.Options.CredentialRef)
	}

	res := &core.Resource{
		ID: "res-" + string(id), Kind: core.KindSingle,
		Files:   []core.ResourceFile{{Index: 0, Path: "a.bin", Size: 2, Selected: true}},
		Headers: map[string]string{"Referer": "https://x", "Authorization": secret},
	}
	if err := st.SaveResource(ctx, id, res); err != nil {
		t.Fatal(err)
	}
	var rawHeaders string
	if err := st.db.QueryRowContext(ctx, `SELECT headers FROM resources WHERE id=?`, res.ID).
		Scan(&rawHeaders); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rawHeaders, "SUPER-SECRET") {
		t.Fatalf("credential leaked into resources.headers: %s", rawHeaders)
	}
}

// T-FR-RSV-06-03: normalized tables must rebuild the full Resource graph.
func TestResourcePersistenceRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id := core.NewID()
	now := time.Now().UnixMilli()
	task := &core.Task{ID: id, State: core.StatePending, Input: "magnet:?xt=x",
		InputKind: core.InputMagnet, CreatedAt: now, UpdatedAt: now}
	if err := st.InsertTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	res := &core.Resource{
		ID: "res-" + string(id), Kind: core.KindTorrent, Engine: core.EngineGo,
		Files: []core.ResourceFile{
			{Index: 0, Path: "S01E01.mkv", Size: 100, Selected: true},
			{Index: 1, Path: "S01E02.mkv", Size: 200, Selected: false},
		},
		Mirrors:    []core.Mirror{{URL: "https://m/a", Priority: 1, Weight: 1}},
		Headers:    map[string]string{"Referer": "https://example.com"},
		Credential: "keychain://ud/cred/1",
		Identity: core.ProtocolIdentity{
			InfoHashV1: "A1", InfoHashV2: "B2", Private: true,
			Trackers:   [][]string{{"https://tracker/announce"}},
			PasskeyRef: "keychain://ud/passkey/1", PeerID: "-GT0001-",
			ED2KFileHash: "E1", AICHRoot: "AICH", UserHash: "UH",
			IdentKeyRef: "file://ident.pem", CreditsRef: "file://clients.met",
			VariantID: "1080p", IsAdaptive: true,
		},
		PostProcess: []core.ProcessSpec{{Type: "remux", Target: "out.mp4"}},
		Limits:      core.TaskLimits{MaxConnections: 8, ChunkSize: 4 << 20},
	}
	res.Checksum = &core.Checksum{Algo: "sha256", Whole: "WHOLE", Pieces: []string{"p0", "p1"}}
	if err := st.SaveResource(ctx, id, res); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetResource(ctx, res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res, got) {
		t.Fatalf("resource mismatch\nwant: %+v\ngot:  %+v", res, got)
	}
}

func TestChunksAndBitfield(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id := core.NewID()
	now := time.Now().UnixMilli()
	task := &core.Task{ID: id, State: core.StateRunning, Input: "https://x/a",
		InputKind: core.InputURL, CreatedAt: now, UpdatedAt: now}
	if err := st.InsertTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	res := &core.Resource{ID: "res-" + string(id), Kind: core.KindSingle, Engine: core.EngineGo,
		Files: []core.ResourceFile{{Index: 0, Path: "a", Size: 2, Selected: true}}}
	if err := st.SaveResource(ctx, id, res); err != nil {
		t.Fatal(err)
	}
	n, err := st.CreateChunks(ctx, id, 0, 5, 2)
	if err != nil || n != 3 {
		t.Fatalf("chunks=%d err=%v", n, err)
	}
	n2, _ := st.CreateChunks(ctx, id, 0, 5, 2)
	if n2 != 3 {
		t.Fatalf("CreateChunks is not idempotent: %d", n2)
	}
	c, ok, err := st.Next(ctx, id)
	if !ok || err != nil || c.State != core.ChunkAssigned || c.Attempt != 1 {
		t.Fatalf("claim failed: %+v ok=%v err=%v", c, ok, err)
	}
	payload := []byte("ab")
	sum := sha256.Sum256(payload)
	if err := st.WriteChunkDone(ctx, c, res.ID, hex.EncodeToString(sum[:]), 0); err != nil {
		t.Fatal(err)
	}
	digest, _ := st.ChunkDigest(ctx, res.ID, 0, 0)
	if digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest mismatch: %s", digest)
	}
	if err := st.Verified(ctx, c); err != nil {
		t.Fatal(err)
	}
	// BEP3 / aria2 bitfield: ceil(numPieces/8) bytes, MSB-first.
	// 3 pieces -> 1 byte, first piece complete -> 0b1000_0000 -> "80".
	bf, count, verified, err := st.Bitfield(ctx, id)
	if err != nil || count != 3 || verified != 1 || bf != "80" {
		t.Fatalf("bitfield=%s count=%d verified=%d err=%v", bf, count, verified, err)
	}
	pending, _ := st.PendingCount(ctx, id)
	if pending != 2 {
		t.Fatalf("pending=%d", pending)
	}
}

// T-NFR-REL-01-02: recovery trusts only chunk digests + actual .part bytes.
func TestCrashRecoveryResetsAndVerifies(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id := core.NewID()
	now := time.Now().UnixMilli()
	task := &core.Task{ID: id, State: core.StateRunning, Input: "https://x/a",
		InputKind: core.InputURL, CreatedAt: now, UpdatedAt: now}
	if err := st.InsertTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	res := &core.Resource{ID: "res-" + string(id), Kind: core.KindSingle,
		Files: []core.ResourceFile{{Index: 0, Path: "a", Size: 2, Selected: true}}}
	_ = st.SaveResource(ctx, id, res)
	_, _ = st.CreateChunks(ctx, id, 0, 4, 2)
	temp := filepath.Join(t.TempDir(), "a.part")
	_ = st.SetOption(ctx, id, "temp_path", temp)

	c0, _, _ := st.Next(ctx, id)
	c1, _, _ := st.Next(ctx, id)
	good := []byte("AB")
	bad := []byte("CD")
	sum0 := sha256.Sum256(good)
	sum1 := sha256.Sum256([]byte("ZZ")) // recorded hash will NOT match on-disk bytes
	_ = st.WriteChunkDone(ctx, c0, res.ID, hex.EncodeToString(sum0[:]), 0)
	_ = st.WriteChunkDone(ctx, c1, res.ID, hex.EncodeToString(sum1[:]), 0)
	if err := os.WriteFile(temp, append(good, bad...), 0o600); err != nil {
		t.Fatal(err)
	}
	ids, err := st.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != id {
		t.Fatalf("unexpected recovered ids: %v", ids)
	}
	chunks, _ := st.Chunks(ctx, id)
	if chunks[0].State != core.ChunkDone || chunks[1].State != core.ChunkPending {
		t.Fatalf("recovery states: %+v", chunks)
	}
}

func TestBackup(t *testing.T) {
	st := newTestStore(t)
	dest := filepath.Join(t.TempDir(), "backup.db")
	if err := st.Backup(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dest); err != nil || info.Size() == 0 {
		t.Fatalf("backup missing: %v", err)
	}
}

// T-NFR-REL-01-03 / docs/13 §8 step 5: the rollback path is
// "stop daemon -> restore backup DB -> verify .part and final file".
// This drill snapshots a live task, simulates a bad upgrade that bumps
// schema_version and destroys chunk progress, then restores the snapshot and
// proves crash recovery reuses exactly the chunks whose bytes still verify.
func TestBackupRestoreRollbackDrill(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	livePath := filepath.Join(dir, "live.db")
	backupPath := filepath.Join(dir, "backup.db")
	tempPath := filepath.Join(dir, "a.bin.part")

	st, err := Open(livePath, core.NewEventBus(64))
	if err != nil {
		t.Fatal(err)
	}
	id := core.NewID()
	now := time.Now().UnixMilli()
	if err := st.InsertTask(ctx, &core.Task{
		ID: id, State: core.StateRunning, Input: "https://x/a.bin",
		InputKind: core.InputURL, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	res := &core.Resource{ID: "res-" + string(id), Kind: core.KindSingle,
		Files: []core.ResourceFile{{Index: 0, Path: "a.bin", Size: 4, Selected: true}}}
	if err := st.SaveResource(ctx, id, res); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateChunks(ctx, id, 0, 4, 2); err != nil {
		t.Fatal(err)
	}
	if err := st.SetOption(ctx, id, "temp_path", tempPath); err != nil {
		t.Fatal(err)
	}
	good, bad := []byte("AB"), []byte("CD")
	c0, _, _ := st.Next(ctx, id)
	c1, _, _ := st.Next(ctx, id)
	sum0 := sha256.Sum256(good)
	sum1 := sha256.Sum256([]byte("ZZ")) // deliberately wrong on-disk bytes
	_ = st.WriteChunkDone(ctx, c0, res.ID, hex.EncodeToString(sum0[:]), 0)
	_ = st.WriteChunkDone(ctx, c1, res.ID, hex.EncodeToString(sum1[:]), 0)
	if err := os.WriteFile(tempPath, append(good, bad...), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. Pre-upgrade consistent snapshot (WAL checkpoint + VACUUM INTO).
	if err := st.Backup(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(tempPath)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Bad upgrade: schema bumped, task cancelled, all chunk progress lost.
	_ = st.UpdateState(ctx, id, StateUpdate{To: core.StateCanceled})
	_, _ = st.db.ExecContext(ctx, `UPDATE app_meta SET value='2' WHERE key='schema_version'`)
	_, _ = st.db.ExecContext(ctx, `UPDATE chunks SET state='pending'`)
	if v, _ := st.SchemaVersion(ctx); v != 2 {
		t.Fatalf("simulated upgrade did not bump schema_version: %d", v)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// 3. Rollback: stop daemon, restore backup DB, keep .part untouched.
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(livePath + suffix)
	}
	snapshot, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(livePath, snapshot, 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(tempPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf(".part changed during rollback: err=%v", err)
	}

	// 4. Restart: migration is idempotent, schema version restored, crash
	//    recovery reuses only chunks whose bytes still hash correctly.
	restored, err := Open(livePath, core.NewEventBus(64))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if v, _ := restored.SchemaVersion(ctx); v != 1 {
		t.Fatalf("schema_version after rollback = %d, want 1", v)
	}
	ids, err := restored.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != id {
		t.Fatalf("recovered ids = %v, want [%s]", ids, id)
	}
	chunks, err := restored.Chunks(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 || chunks[0].State != core.ChunkDone || chunks[1].State != core.ChunkPending {
		t.Fatalf("chunk states after rollback: %+v", chunks)
	}
	bf, count, verified, err := restored.Bitfield(ctx, id)
	if err != nil || count != 2 || verified != 0 || bf != "80" {
		t.Fatalf("bitfield=%s count=%d verified=%d err=%v", bf, count, verified, err)
	}
}
