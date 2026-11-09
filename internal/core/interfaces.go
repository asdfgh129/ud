package core

import (
	"context"
	"net"
)

// URLGuard is the core-side abstraction of security.SSRFGuard.
//
// P0 refinement (see CHANGELOG 0.1.0 / docs/05 §2): the original LLD had
// `Guard *security.SSRFGuard` inside a core struct, which would force core to
// import an implementation package and violate the dependency rule in
// docs/05 §1. core now owns the interface; security provides the implementation.
type URLGuard interface {
	// Check validates one untrusted URL, resolves DNS once and pins the result.
	Check(ctx context.Context, raw string) error
	// DialContext connects only to an IP already validated by Check (DNS rebinding defense).
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	// MaxRedirects is the per-hop limit enforced by the HTTP transport.
	MaxRedirects() int
}

// ResolveContext is handed to a Resolver. Only credential REFERENCES travel here.
type ResolveContext struct {
	Ctx        context.Context
	Credential CredentialRef
	Options    map[string]any
	Guard      URLGuard
}

// Resolver turns one class of input into a complete Resource.
type Resolver interface {
	Name() string
	Match(in Input) bool
	Resolve(ctx ResolveContext, in Input) (*Resource, error)
}

// Transport only moves bytes into a ChunkWriter. It never owns queueing,
// rate limits, state transitions or file layout (ADR-0002).
type Transport interface {
	Name() string
	Supports(r *Resource) bool
	Probe(ctx context.Context, r *Resource) (*ProbeResult, error)
	Run(ctx context.Context, r *Resource, w ChunkWriter, b Budget) error
}

// PostProcessor input MUST be local file paths (ADR-0012). No URL, io.Reader
// or network stream is allowed anywhere in this signature.
type PostProcessor interface {
	Name() string
	Process(ctx context.Context, in LocalFiles, out OutputSpec) (*ProcessResult, error)
}

// ChunkWriter is the scheduler-owned sink for all transports.
//
// P0 refinement of docs/05 §2: Next/Release were added so the SQLite `chunks`
// table stays the single source of truth. A transport NEVER invents chunk
// boundaries or writes task state directly.
type ChunkWriter interface {
	// Next atomically claims the next pending chunk (pending -> assigned).
	Next(ctx context.Context) (Chunk, bool, error)
	// WriteChunk persists bytes at the chunk offset and records its digest.
	WriteChunk(ctx context.Context, c Chunk, p []byte) error
	// Verified marks a chunk as hash-verified.
	Verified(ctx context.Context, c Chunk, sum Hash) error
	// Release handles a failed chunk: retry (back to pending) or terminal failure.
	Release(ctx context.Context, c Chunk, err error) error
}

// Budget is a rate budget obtained from the one and only Limiter.
type Budget interface {
	Consume(ctx context.Context, n int) error
}

// Limiter is the process-wide唯一 authority for rates (docs/04 §5.3).
type Limiter interface {
	Acquire(ctx context.Context, scope Scope, n int64) (Budget, error)
	SetGlobal(rate BytesPerSec)
	GlobalRate() BytesPerSec
}

// Scheduler is the only component allowed to change Task state.
type Scheduler interface {
	Create(ctx context.Context, in Input, opt TaskOptions) (TaskID, error)
	Pause(ctx context.Context, id TaskID) error
	Resume(ctx context.Context, id TaskID) error
	Cancel(ctx context.Context, id TaskID) error
	SetOption(ctx context.Context, id TaskID, opt TaskOptions) error
}

// ControlPlane is the full surface consumed by the REST/WS/CLI adapters.
// api depends on core + this interface only (docs/05 §1).
type ControlPlane interface {
	Scheduler
	Get(ctx context.Context, id TaskID) (*TaskDetail, error)
	List(ctx context.Context, f TaskFilter) ([]*Task, int, error)
	Delete(ctx context.Context, id TaskID, withFiles bool) error
	ResolveOnly(ctx context.Context, in Input, opt TaskOptions) (*Resource, error)
	Stats(ctx context.Context) (Stats, error)
	Health(ctx context.Context) Health
	Capabilities() Capabilities
	Recover(ctx context.Context) (int, error)
	Close() error
}

// CredentialStore is the boundary to the OS keychain (ADR-0015). P0 ships an
// in-process reference implementation; OS adapters are a later work item.
type CredentialStore interface {
	Put(ctx context.Context, secret string) (CredentialRef, error)
	Get(ctx context.Context, ref CredentialRef) (string, error)
	Delete(ctx context.Context, ref CredentialRef) error
}
