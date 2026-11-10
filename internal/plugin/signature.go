package plugin

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/asdfgh129/ud/internal/core"
)

// Signature scheme (docs/08 §2, ADR-0014 §3):
//
//	payload = SHA256( canonicalManifestJSON || 0x00 || entryScriptBytes )
//	signature = "ed25519:" || base64( Ed25519.Sign(priv, payload) )
//
// canonicalManifestJSON is the manifest with its "signature" field removed and
// re-marshalled by encoding/json (map keys sorted), so whitespace and key
// order in the source file are irrelevant. The entry script is part of the
// payload: changing a single byte of a rule invalidates the signature.
func Payload(manifestRaw, script []byte) ([]byte, error) {
	var obj map[string]any
	if err := json.Unmarshal(manifestRaw, &obj); err != nil {
		return nil, core.Err(core.E_INVALID_INPUT, "manifest is not a JSON object: %v", err)
	}
	delete(obj, "signature")
	canonical, err := json.Marshal(obj)
	if err != nil {
		return nil, core.Err(core.E_INVALID_INPUT, "canonicalise manifest: %v", err)
	}
	h := sha256.New()
	_, _ = h.Write(canonical)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(script)
	return h.Sum(nil), nil
}

// Sign produces the manifest signature string for an official rule pack.
func Sign(manifestRaw, script []byte, priv ed25519.PrivateKey) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", core.Err(core.E_INVALID_INPUT, "invalid Ed25519 private key length %d", len(priv))
	}
	payload, err := Payload(manifestRaw, script)
	if err != nil {
		return "", err
	}
	return "ed25519:" + base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload)), nil
}

// Verify checks a rule pack against any configured trusted public key. It
// fails closed when the signature is missing, malformed, or no key matches.
func Verify(manifestRaw, script []byte, sig string, keys []ed25519.PublicKey) error {
	if !strings.HasPrefix(sig, "ed25519:") {
		return core.Err(core.E_RESOLVE_SSRF_BLOCKED, "rule pack has no Ed25519 signature")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sig, "ed25519:"))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return core.Err(core.E_RESOLVE_SSRF_BLOCKED, "malformed Ed25519 signature")
	}
	payload, err := Payload(manifestRaw, script)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return core.Err(core.E_RESOLVE_SSRF_BLOCKED,
			"signature verification requested but no trusted Ed25519 key is configured")
	}
	for _, k := range keys {
		if ed25519.Verify(k, payload, raw) {
			return nil
		}
	}
	return core.Err(core.E_RESOLVE_SSRF_BLOCKED, "rule pack signature does not match any trusted key")
}

// ParsePublicKeys decodes base64-encoded raw 32-byte Ed25519 public keys.
func ParsePublicKeys(encoded []string) ([]ed25519.PublicKey, error) {
	out := make([]ed25519.PublicKey, 0, len(encoded))
	for i, s := range encoded {
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err != nil || len(b) != ed25519.PublicKeySize {
			return nil, core.Err(core.E_INVALID_INPUT, "trustedKeys[%d] is not a base64 Ed25519 public key", i)
		}
		out = append(out, ed25519.PublicKey(b))
	}
	return out, nil
}

// ParsePrivateKey decodes a base64 raw 64-byte Ed25519 private key (key files
// are never written by the daemon, only used by the offline signing CLI).
func ParsePrivateKey(encoded string) (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(b) != ed25519.PrivateKeySize {
		return nil, core.Err(core.E_INVALID_INPUT, "not a base64 Ed25519 private key")
	}
	return ed25519.PrivateKey(b), nil
}

// GenerateKeyPair is used by `udctl plugin keygen`.
func GenerateKeyPair() (publicB64, privateB64 string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate Ed25519 key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(priv), nil
}

// WriteManifest pretty-prints a manifest to disk with 0600 permissions.
func WriteManifest(path string, m *Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// writeSignedManifest preserves every authored key and only sets "signature".
func writeSignedManifest(path string, raw []byte, sig string) error {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return core.Err(core.E_INVALID_INPUT, "manifest is not a JSON object: %v", err)
	}
	obj["signature"] = sig
	b, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal signed manifest: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("write signed manifest: %w", err)
	}
	return nil
}
