// Package postproc implements the ADR-0012 contract: PostProcessor input is
// ONLY local file paths, output is a .part file, and the core owns the rename.
package postproc

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/asdfgh129/ud/internal/core"
)

// Passthrough is the P0 built-in processor: no remux/transcode, so it returns
// the already-downloaded .part paths for the core to commit atomically.
// Remux (pure Go) and Transcode (ffmpeg) plug into the same interface in P2.
type Passthrough struct{}

func (Passthrough) Name() string { return "none" }

func (Passthrough) Process(ctx context.Context, in core.LocalFiles, out core.OutputSpec) (*core.ProcessResult, error) {
	if len(in.Paths) == 0 {
		return nil, core.Err(core.E_POSTPROCESS_FAILED, "no local input files")
	}
	for _, p := range in.Paths {
		if p == "" || strings.Contains(p, "://") {
			return nil, core.Err(core.E_POSTPROCESS_FAILED,
				"PostProcessor accepts local file paths only (ADR-0012)")
		}
		info, err := os.Stat(p)
		if err != nil {
			return nil, core.Err(core.E_POSTPROCESS_FAILED, "input file unavailable: %v", err)
		}
		if info.IsDir() {
			return nil, core.Err(core.E_POSTPROCESS_FAILED, "input path is a directory")
		}
		if err := ctx.Err(); err != nil {
			return nil, core.Err(core.E_POSTPROCESS_FAILED, "%v", err)
		}
	}
	return &core.ProcessResult{
		Outputs:   append([]string(nil), in.Paths...),
		TempPaths: append([]string(nil), in.Paths...),
	}, nil
}

type Registry struct {
	mu    sync.RWMutex
	items map[string]core.PostProcessor
}

func NewRegistry() *Registry {
	r := &Registry{items: make(map[string]core.PostProcessor)}
	r.Register(Passthrough{})
	return r
}

func (r *Registry) Register(p core.PostProcessor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[p.Name()] = p
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.items))
	for k := range r.items {
		out = append(out, k)
	}
	return out
}

// Get resolves a ProcessSpec type. Unknown processors degrade explicitly;
// P0 has no sidecar implementations (ADR-0008 / FR-PLT-06).
func (r *Registry) Get(name string) (core.PostProcessor, error) {
	if name == "" || name == "passthrough" {
		name = "none"
	}
	r.mu.RLock()
	p, ok := r.items[name]
	r.mu.RUnlock()
	if !ok {
		return nil, core.Err(core.E_CAPABILITY_UNAVAILABLE,
			"postprocessor %q is not available in P0", name)
	}
	return p, nil
}
