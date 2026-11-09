package security

import (
	"context"
	"sync"

	"github.com/asdfgh129/ud/internal/core"
)

// MemoryCredentialStore is the P0 reference implementation of
// core.CredentialStore. WBS 3 (docs/14 §1) delivers the keychain INTERFACE in
// P0; the OS adapters (Keychain / Credential Manager / libsecret) are later.
//
// Secrets live only in this process' memory: they are never written to SQLite,
// events, logs or REST responses, satisfying NFR-SEC-02 for the P0 baseline.
// Nothing survives a restart, which is intentional for a contract-only stub.
type MemoryCredentialStore struct {
	mu    sync.RWMutex
	items map[core.CredentialRef]string
}

func NewMemoryCredentialStore() *MemoryCredentialStore {
	return &MemoryCredentialStore{items: make(map[core.CredentialRef]string)}
}

func (s *MemoryCredentialStore) Put(_ context.Context, secret string) (core.CredentialRef, error) {
	if secret == "" {
		return "", core.Err(core.E_INVALID_INPUT, "empty credential")
	}
	ref := core.CredentialRef("mem://ud/cred/" + string(core.NewID()))
	s.mu.Lock()
	s.items[ref] = secret
	s.mu.Unlock()
	return ref, nil
}

func (s *MemoryCredentialStore) Get(_ context.Context, ref core.CredentialRef) (string, error) {
	s.mu.RLock()
	v, ok := s.items[ref]
	s.mu.RUnlock()
	if !ok {
		return "", core.Err(core.E_INVALID_INPUT, "credential reference not found")
	}
	return v, nil
}

func (s *MemoryCredentialStore) Delete(_ context.Context, ref core.CredentialRef) error {
	s.mu.Lock()
	delete(s.items, ref)
	s.mu.Unlock()
	return nil
}
