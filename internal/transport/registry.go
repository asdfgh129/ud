// Package transport registers Transport implementations. It owns no queue,
// rate limit or task state (docs/04 §5 / ADR-0002).
package transport

import (
	"sync"

	"github.com/asdfgh129/ud/internal/core"
)

type Registry struct {
	mu    sync.RWMutex
	items map[string]core.Transport
	order []string
}

func NewRegistry() *Registry {
	return &Registry{items: make(map[string]core.Transport)}
}

func (r *Registry) Register(t core.Transport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[t.Name()]; !exists {
		r.order = append(r.order, t.Name())
	}
	r.items[t.Name()] = t
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.order...)
}

// Select returns the first transport that supports this Resource.
func (r *Registry) Select(res *core.Resource) (core.Transport, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, name := range r.order {
		if t := r.items[name]; t.Supports(res) {
			return t, nil
		}
	}
	return nil, core.Err(core.E_CAPABILITY_UNAVAILABLE,
		"no transport available for resource kind %q", res.Kind)
}
