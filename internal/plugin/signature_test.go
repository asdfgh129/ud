package plugin

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func keyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, string, string) {
	t.Helper()
	pubB64, privB64, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	pubRaw, _ := base64.StdEncoding.DecodeString(pubB64)
	priv, err := ParsePrivateKey(privB64)
	if err != nil {
		t.Fatal(err)
	}
	return ed25519.PublicKey(pubRaw), priv, pubB64, privB64
}

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv, pubB64, _ := keyPair(t)
	m := baseManifest()
	raw := mustJSON(t, &m)
	script := []byte(happyScript)
	sig, err := Sign(raw, script, priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(raw, script, sig, []ed25519.PublicKey{pub}); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := Verify(raw, append(script, '\n'), sig, []ed25519.PublicKey{pub}); err == nil {
		t.Fatal("tampered script must be rejected")
	}
	tampered := baseManifest()
	tampered.Version = "9.9.9"
	if err := Verify(mustJSON(t, &tampered), script, sig, []ed25519.PublicKey{pub}); err == nil {
		t.Fatal("tampered manifest must be rejected")
	}
	otherPub, _, _, _ := keyPair(t)
	if err := Verify(raw, script, sig, []ed25519.PublicKey{otherPub, pub}); err != nil {
		t.Fatal("one matching key in the trust list must be enough")
	}
	if err := Verify(raw, script, sig, []ed25519.PublicKey{otherPub}); err == nil {
		t.Fatal("untrusted key must be rejected")
	}
	if err := Verify(raw, script, sig, nil); err == nil {
		t.Fatal("empty trust list must fail closed")
	}
	if err := Verify(raw, script, "", []ed25519.PublicKey{pub}); err == nil {
		t.Fatal("missing signature must fail closed")
	}
	if err := Verify(raw, script, "ed25519:%%%", []ed25519.PublicKey{pub}); err == nil {
		t.Fatal("malformed base64 must fail closed")
	}
	if err := Verify(raw, script, "ed25519:"+base64.StdEncoding.EncodeToString(make([]byte, 8)),
		[]ed25519.PublicKey{pub}); err == nil {
		t.Fatal("short signature must fail closed")
	}
	// Whitespace / key order must not affect the canonical payload.
	var obj map[string]any
	_ = json.Unmarshal(raw, &obj)
	reordered, _ := json.MarshalIndent(obj, "", "    ")
	if err := Verify(reordered, script, sig, []ed25519.PublicKey{pub}); err != nil {
		t.Fatalf("canonicalisation not whitespace-independent: %v", err)
	}
	_ = pubB64
}

func TestPayloadRejectsNonObjectManifest(t *testing.T) {
	if _, err := Payload([]byte("["), nil); err == nil {
		t.Fatal("array manifest must be rejected")
	}
}

func TestParseKeysHelpers(t *testing.T) {
	pub, priv, pubB64, privB64 := keyPair(t)
	keys, err := ParsePublicKeys([]string{" " + pubB64 + " "})
	if err != nil || len(keys) != 1 || !keys[0].Equal(pub) {
		t.Fatalf("ParsePublicKeys: %v", err)
	}
	if _, err := ParsePublicKeys([]string{"not-base64!!"}); err == nil {
		t.Fatal("bad public key must be rejected")
	}
	if _, err := ParsePublicKeys([]string{base64.StdEncoding.EncodeToString(make([]byte, 8))}); err == nil {
		t.Fatal("short public key must be rejected")
	}
	parsedPriv, err := ParsePrivateKey(privB64)
	if err != nil || !parsedPriv.Equal(priv) {
		t.Fatalf("ParsePrivateKey: %v", err)
	}
	if _, err := ParsePrivateKey("nope"); err == nil {
		t.Fatal("bad private key must be rejected")
	}
	if _, err := Sign(nil, nil, ed25519.PrivateKey(make([]byte, 3))); err == nil {
		t.Fatal("short signing key must be rejected")
	}
}

func TestWriteManifestAndSignedManifest(t *testing.T) {
	dir := t.TempDir()
	m := baseManifest()
	p := filepath.Join(dir, "manifest.json")
	if err := WriteManifest(p, &m); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseManifest(readFile(t, p)); err != nil {
		t.Fatalf("written manifest must parse: %v", err)
	}
	raw := readFile(t, p)
	if err := writeSignedManifest(p, raw, "ed25519:abc"); err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(readFile(t, p), &obj); err != nil {
		t.Fatal(err)
	}
	if obj["signature"] != "ed25519:abc" || obj["name"] != m.Name {
		t.Fatalf("signed manifest lost fields: %+v", obj)
	}
	if err := writeSignedManifest(p, []byte("["), "x"); err == nil {
		t.Fatal("non-object manifest must be rejected")
	}
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSignPackRoundTrip(t *testing.T) {
	pub, _, _, privB64 := keyPair(t)
	dir := t.TempDir()
	m := baseManifest()
	writePack(t, dir, &m, happyScript)
	if _, err := VerifyPack(dir, []ed25519.PublicKey{pub}, true, false); err == nil {
		t.Fatal("unsigned pack must be rejected by default")
	}
	if _, err := VerifyPack(dir, nil, true, true); err != nil {
		t.Fatalf("insecure verify should pass: %v", err)
	}
	signed, err := SignPack(dir, privB64)
	if err != nil {
		t.Fatal(err)
	}
	if signed.Signature == "" {
		t.Fatal("signature was not written")
	}
	if _, err := VerifyPack(dir, []ed25519.PublicKey{pub}, true, false); err != nil {
		t.Fatalf("signed pack rejected: %v", err)
	}
	// Re-signing must stay valid (idempotent).
	if _, err := SignPack(dir, privB64); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPack(dir, []ed25519.PublicKey{pub}, true, false); err != nil {
		t.Fatalf("re-signed pack rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(happyScript+"\n// tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPack(dir, []ed25519.PublicKey{pub}, true, false); err == nil {
		t.Fatal("tampered signed pack must be rejected")
	}
	if _, err := SignPack(dir, "bad-key"); err == nil {
		t.Fatal("bad signing key must be rejected")
	}
	if _, err := SignPack(filepath.Join(dir, "missing"), privB64); err == nil {
		t.Fatal("missing pack must be rejected")
	}
}

func TestVerifyPackCompileErrors(t *testing.T) {
	dir := t.TempDir()
	m := baseManifest()
	writePack(t, dir, &m, "function resolve( {")
	if _, err := VerifyPack(dir, nil, true, true); err == nil {
		t.Fatal("broken JS must be rejected")
	}
	m = baseManifest()
	writePack(t, dir, &m, "export function resolve(){ const x = 1; return x; }")
	if _, err := VerifyPack(dir, nil, true, true); err == nil {
		t.Fatal("forbidden syntax must be rejected")
	}
	if _, err := VerifyPack(filepath.Join(dir, "missing"), nil, true, true); err == nil {
		t.Fatal("missing dir must be rejected")
	}
}
