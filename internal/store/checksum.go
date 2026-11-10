package store

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"
	"io"
	"os"
	"strings"

	"github.com/asdfgh129/ud/internal/core"
)

// newDigester maps a Metalink / RFC 6249 algorithm name to a standard-library
// hash. No third-party dependency and no cgo (ADR-0001). An unknown algorithm
// is a hard error: silently falling back to sha256 would make a manifest's
// digests "pass" against the wrong algorithm.
func newDigester(algo string) (hash.Hash, error) {
	normalized := strings.ToLower(strings.ReplaceAll(algo, "-", ""))
	switch normalized {
	case "", "sha256": // P0 callers omit Algo; sha256 stays the default.
		return sha256.New(), nil
	case "sha1":
		return sha1.New(), nil
	case "md5":
		return md5.New(), nil
	case "sha512":
		return sha512.New(), nil
	default:
		return nil, core.Err(core.E_INVALID_INPUT, "unsupported checksum algorithm %q", algo)
	}
}

// WholeFileChecksumAlgo hashes an entire file with the algorithm declared by
// the manifest. It is the algorithm-aware counterpart of the P0 sha256-only
// WholeFileChecksum (kept for crash-recovery callers).
func WholeFileChecksumAlgo(ctx context.Context, path, algo string) (string, error) {
	h, err := newDigester(algo)
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(h, &ctxReader{ctx: ctx, r: f}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyPieceChecksums compares each chunk's on-disk byte range against the
// piece digests declared by a Metalink manifest. ADR-0006: a failed piece must
// be re-fetched individually, never the whole file. Callers are responsible
// for releasing the returned chunks.
//
// The chunk boundaries MUST match the manifest's piece length; the Scheduler
// pins Limits.ChunkSize to that value before creating chunks.
func (s *Store) VerifyPieceChecksums(ctx context.Context, id core.TaskID, tempPath, algo string, pieces []string) ([]core.Chunk, error) {
	if len(pieces) == 0 {
		return nil, nil
	}
	chunks, err := s.Chunks(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(chunks) != len(pieces) {
		return nil, core.Err(core.E_CHECKSUM_MISMATCH,
			"manifest declares %d pieces but the task has %d chunks (size or piece length mismatch)", len(pieces), len(chunks))
	}
	f, err := os.Open(tempPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var bad []core.Chunk
	for _, c := range chunks {
		if c.Index < 0 || c.Index >= len(pieces) {
			return nil, core.Err(core.E_CHECKSUM_MISMATCH, "chunk index %d outside manifest piece table", c.Index)
		}
		h, err := newDigester(algo)
		if err != nil {
			return nil, err
		}
		if c.Length > 0 {
			r := io.NewSectionReader(f, c.Start, c.Length)
			if _, err := io.Copy(h, r); err != nil {
				return nil, err
			}
		}
		got := hex.EncodeToString(h.Sum(nil))
		// State correctness is already covered by VerifyAllChunks; here we only
		// report digest mismatches so the Scheduler can de-duplicate the two.
		if !strings.EqualFold(got, pieces[c.Index]) {
			bad = append(bad, c)
		}
	}
	return bad, nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
