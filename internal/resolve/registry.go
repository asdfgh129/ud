// Package resolve hosts Resolver registration and the P0 HTTP direct resolver.
package resolve

import (
	"sync"

	"github.com/asdfgh129/ud/internal/core"
)

// Registry resolves inputs by priority: the first matching Resolver wins.
// Built-in resolvers are registered before plugins and can never be overridden
// by a plugin (docs/05 §10).
type Registry struct {
	mu        sync.RWMutex
	resolvers []core.Resolver
}

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) Register(x core.Resolver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolvers = append(r.resolvers, x)
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.resolvers))
	for _, x := range r.resolvers {
		out = append(out, x.Name())
	}
	return out
}

// Resolve returns a complete Resource or a classified error.
func (r *Registry) Resolve(ctx core.ResolveContext, in core.Input) (*core.Resource, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, x := range r.resolvers {
		if x.Match(in) {
			return x.Resolve(ctx, in)
		}
	}
	return nil, core.Err(core.E_RESOLVE_FAILED, "no resolver registered for %s", in.Kind)
}
