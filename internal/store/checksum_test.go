package store

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asdfgh129/ud/internal/core"
)

func checksumFixture(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Reference digests are computed with the stdlib directly (not via
// newDigester) so a wrong algorithm mapping cannot make both sides agree.
func referenceDigest(algo string, b []byte) string {
	var h hash.Hash
	switch strings.ToLower(strings.ReplaceAll(algo, "-", "")) {
	case "", "sha256":
		h = sha256.New()
	case "sha1":
		h = sha1.New()
	case "md5":
		h = md5.New()
	case "sha512":
		h = sha512.New()
	default:
		panic("referenceDigest: unsupported algorithm " + algo)
	}
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

func TestNewDigesterAlgorithms(t *testing.T) {
	for _, tc := range []struct {
		algo string
		size int
	}{
		{"", 32}, {"sha256", 32}, {"SHA-256", 32}, {"sha-256", 32},
		{"sha-1", 20}, {"SHA1", 20}, {"md5", 16}, {"sha-512", 64}, {"SHA512", 64},
	} {
		h, err := newDigester(tc.algo)
		if err != nil {
			t.Fatalf("%q: %v", tc.algo, err)
		}
		if h.Size() != tc.size {
			t.Fatalf("%q: hash size = %d, want %d", tc.algo, h.Size(), tc.size)
		}
	}
	for _, bad := range []string{"crc32", "sha3-256", "blake2b"} {
		_, err := newDigester(bad)
		if err == nil || core.AsError(err).Code != core.E_INVALID_INPUT {
			t.Fatalf("%q must be a classified error, got %v", bad, err)
		}
	}
}

func TestWholeFileChecksumAlgo(t *testing.T) {
	data := []byte("universal-downloader metalink fixture: 0123456789")
	p := checksumFixture(t, data)
	ctx := context.Background()
	for _, algo := range []string{"", "sha-256", "SHA-1", "md5", "sha-512"} {
		got, err := WholeFileChecksumAlgo(ctx, p, algo)
		if err != nil {
			t.Fatalf("%q: %v", algo, err)
		}
		if want := referenceDigest(algo, data); got != want {
			t.Fatalf("%q: got %s want %s", algo, got, want)
		}
	}
	// Empty file still produces the well-known empty digest.
	empty := checksumFixture(t, nil)
	got, err := WholeFileChecksumAlgo(ctx, empty, "sha-256")
	if err != nil || got != referenceDigest("", nil) {
		t.Fatalf("empty file: %s %v", got, err)
	}
	// Error paths.
	if _, err := WholeFileChecksumAlgo(ctx, p, "crc32"); core.AsError(err).Code != core.E_INVALID_INPUT {
		t.Fatalf("unknown algorithm must be rejected, got %v", err)
	}
	if _, err := WholeFileChecksumAlgo(ctx, p+".missing", "sha-256"); err == nil {
		t.Fatal("missing file must be reported")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := WholeFileChecksumAlgo(canceled, p, "sha-256"); err == nil {
		t.Fatal("canceled context must abort the read")
	}
}

func TestCtxReaderStopsOnCancel(t *testing.T) {
	r := &ctxReader{ctx: context.Background(), r: strings.NewReader("abc")}
	buf := make([]byte, 3)
	if n, err := r.Read(buf); err != nil || n != 3 || string(buf) != "abc" {
		t.Fatalf("live context read: %d %q %v", n, buf, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	r2 := &ctxReader{ctx: canceled, r: strings.NewReader("abc")}
	if _, err := r2.Read(buf); err == nil {
		t.Fatal("canceled ctxReader must return the context error")
	}
}

func TestVerifyPieceChecksums(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	id := mustInsertTask(t, st, core.StateRunning, core.InputMetalink, "local.meta4")
	data := []byte("0123456789ABCDEF") // four 4-byte pieces
	if _, err := st.CreateChunks(ctx, id, 0, int64(len(data)), 4); err != nil {
		t.Fatal(err)
	}
	p := checksumFixture(t, data)

	pieces := func() []string {
		return []string{
			referenceDigest("sha-1", data[0:4]),
			referenceDigest("sha-1", data[4:8]),
			referenceDigest("sha-1", data[8:12]),
			referenceDigest("sha-1", data[12:16]),
		}
	}
	ps := pieces()
	ps[1] = strings.ToUpper(ps[1]) // comparison must be case-insensitive
	bad, err := st.VerifyPieceChecksums(ctx, id, p, "sha-1", ps)
	if err != nil || len(bad) != 0 {
		t.Fatalf("all pieces should verify: %v %+v", err, bad)
	}
	if empty, err := st.VerifyPieceChecksums(ctx, id, p, "sha-1", nil); err != nil || empty != nil {
		t.Fatalf("no pieces must be a no-op: %v %+v", err, empty)
	}

	// One corrupt piece is reported individually, never as a whole-file failure.
	ps = pieces()
	ps[2] = strings.Repeat("0", 40)
	bad, err = st.VerifyPieceChecksums(ctx, id, p, "sha-1", ps)
	if err != nil || len(bad) != 1 || bad[0].Index != 2 {
		t.Fatalf("want exactly the third chunk back: %v %+v", err, bad)
	}

	// Chunk / piece count mismatch, unsupported algorithm, missing temp file.
	for _, tc := range []struct {
		name   string
		algo   string
		pieces []string
		path   string
		code   string
	}{
		{"count", "sha-1", pieces()[:3], p, core.E_CHECKSUM_MISMATCH},
		{"algorithm", "crc32", pieces(), p, core.E_INVALID_INPUT},
		{"missing-file", "sha-1", pieces(), p + ".missing", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := st.VerifyPieceChecksums(ctx, id, tc.path, tc.algo, tc.pieces)
			if err == nil {
				t.Fatal("expected an error")
			}
			if tc.code != "" && core.AsError(err).Code != tc.code {
				t.Fatalf("want %s, got %v", tc.code, err)
			}
		})
	}

	// A chunk index outside the manifest's piece table must never panic.
	if _, err := st.db.ExecContext(ctx, `UPDATE chunks SET idx=9 WHERE task_id=? AND idx=3`, string(id)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.VerifyPieceChecksums(ctx, id, p, "sha-1", pieces()); core.AsError(err).Code != core.E_CHECKSUM_MISMATCH {
		t.Fatalf("out-of-range chunk index must be a mismatch, got %v", err)
	}
}

func TestVerifyPieceChecksumsZeroLengthChunk(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	id := mustInsertTask(t, st, core.StateRunning, core.InputMetalink, "empty.meta4")
	if _, err := st.CreateChunks(ctx, id, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	p := checksumFixture(t, nil)
	bad, err := st.VerifyPieceChecksums(ctx, id, p, "sha-256",
		[]string{referenceDigest("", nil)})
	if err != nil || len(bad) != 0 {
		t.Fatalf("zero-length chunk should hash as empty: %v %+v", err, bad)
	}
}
