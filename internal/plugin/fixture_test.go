package plugin

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asdfgh129/ud/internal/core"
)

const replayFixture = `{
  "responses": {
    "https://example.com/video/1": {
      "status": 200,
      "body": "{\"playUrl\":\"https%3A%2F%2Fcdn.example.com%2Fv%2F1.mp4\"}"
    }
  },
  "cases": [
    {
      "url": "https://example.com/video/1",
      "expect": { "mirrors": ["https://cdn.example.com/v/1.mp4"], "files": ["video.mp4"] }
    },
    {
      "url": "https://example.com/video/2",
      "expect": { "error": "RULE_OUTDATED" }
    }
  ]
}
`

func TestRunFixtureOfflineReplay(t *testing.T) {
	dir := t.TempDir()
	m := baseManifest()
	writePack(t, dir, &m, happyScript)
	fp := filepath.Join(dir, "cases.json")
	if err := os.WriteFile(fp, []byte(replayFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	results, err := RunFixture(dir, fp, nil, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results=%d", len(results))
	}
	for _, r := range results {
		if !r.OK {
			t.Fatalf("case failed: %+v", r)
		}
	}
	if !strings.Contains(results[1].Message, "RULE_OUTDATED") {
		t.Fatalf("expected RULE_OUTDATED message, got %q", results[1].Message)
	}
}

func TestRunFixtureSignatureAndErrors(t *testing.T) {
	dir := t.TempDir()
	m := baseManifest()
	writePack(t, dir, &m, happyScript)
	fp := filepath.Join(dir, "cases.json")
	if err := os.WriteFile(fp, []byte(replayFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RunFixture(dir, fp, nil, true, false); err == nil {
		t.Fatal("unsigned pack must be rejected by fixture runner")
	}
	pub, _, _, privB64 := keyPair(t)
	if _, err := SignPack(dir, privB64); err != nil {
		t.Fatal(err)
	}
	if _, err := RunFixture(dir, fp, []ed25519.PublicKey{pub}, true, false); err != nil {
		t.Fatalf("signed pack should pass: %v", err)
	}
	if _, err := RunFixture(dir, filepath.Join(dir, "missing.json"), []ed25519.PublicKey{pub}, true, false); err == nil {
		t.Fatal("missing fixture must error")
	}
	badJSON := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badJSON, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RunFixture(dir, badJSON, []ed25519.PublicKey{pub}, true, false); err == nil {
		t.Fatal("invalid fixture JSON must error")
	}
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(`{"responses":{},"cases":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RunFixture(dir, empty, []ed25519.PublicKey{pub}, true, false); err == nil {
		t.Fatal("no cases must error")
	}
	// A failing assertion surfaces as a non-OK result, not a runner error.
	mismatch := filepath.Join(dir, "mismatch.json")
	data, _ := json.Marshal(Fixture{
		Responses: map[string]FixtureResponse{
			"https://example.com/video/1": {Body: `{"playUrl":"https%3A%2F%2Fcdn.example.com%2Fv.mp4"}`},
		},
		Cases: []FixtureCase{{URL: "https://example.com/video/1",
			Expect: FixtureExpect{Mirrors: []string{"https://wrong.example.com/x"}}}},
	})
	if err := os.WriteFile(mismatch, data, 0o600); err != nil {
		t.Fatal(err)
	}
	results, err := RunFixture(dir, mismatch, []ed25519.PublicKey{pub}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].OK {
		t.Fatalf("mismatched mirror should fail: %+v", results[0])
	}
}

func TestCheckCaseBranches(t *testing.T) {
	res := &core.Resource{
		Mirrors: []core.Mirror{{URL: "https://a"}},
		Files:   []core.ResourceFile{{Path: "a.bin"}},
	}
	cases := []struct {
		name string
		c    FixtureCase
		want bool
	}{
		{"ok", FixtureCase{Expect: FixtureExpect{Mirrors: []string{"https://a"}, Files: []string{"a.bin"}}}, true},
		{"mirror count", FixtureCase{Expect: FixtureExpect{Mirrors: []string{"a", "b"}}}, false},
		{"mirror value", FixtureCase{Expect: FixtureExpect{Mirrors: []string{"b"}}}, false},
		{"file count", FixtureCase{Expect: FixtureExpect{Files: []string{"a", "b"}}}, false},
		{"file value", FixtureCase{Expect: FixtureExpect{Files: []string{"b"}}}, false},
		{"error expected but none", FixtureCase{Expect: FixtureExpect{Error: "BOOM"}}, false},
	}
	for _, tc := range cases {
		if got := checkCase(tc.c, res, nil).OK; got != tc.want {
			t.Errorf("%s: OK=%v want %v", tc.name, got, tc.want)
		}
	}
	if !checkCase(FixtureCase{Expect: FixtureExpect{Error: "BOOM"}}, nil,
		core.Err(core.E_RESOLVE_FAILED, "BOOM happened")).OK {
		t.Fatal("matching expected error must pass")
	}
	if checkCase(FixtureCase{}, nil, core.Err(core.E_RESOLVE_FAILED, "x")).OK {
		t.Fatal("unexpected error must fail")
	}
}

func TestReplayGuardIsOffline(t *testing.T) {
	g := replayGuard{}
	if err := g.Check(context.Background(), "https://example.com/x"); err != nil {
		t.Fatalf("public https URL should pass the offline guard: %v", err)
	}
	if err := g.Check(context.Background(), "file:///etc/passwd"); err == nil {
		t.Fatal("non-http scheme must be rejected")
	}
	if err := g.Check(context.Background(), "https://x\r\nHost: y"); err == nil {
		t.Fatal("control characters must be rejected")
	}
	if _, err := g.DialContext(context.Background(), "tcp", "x:80"); err == nil {
		t.Fatal("replay guard must never dial")
	}
	if g.MaxRedirects() != 3 {
		t.Fatal("unexpected redirect cap")
	}
}

func TestRunFixtureBadInputURL(t *testing.T) {
	dir := t.TempDir()
	m := baseManifest()
	writePack(t, dir, &m, happyScript)
	fp := filepath.Join(dir, "cases.json")
	data := `{"responses":{},"cases":[{"url":"","expect":{}}]}`
	if err := os.WriteFile(fp, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	results, err := RunFixture(dir, fp, nil, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].OK {
		t.Fatal("empty input URL must fail its case")
	}
	// A broken script is caught before any case runs.
	m2 := baseManifest()
	writePack(t, dir, &m2, "function resolve( {")
	if _, err := RunFixture(dir, fp, nil, true, true); err == nil {
		t.Fatal("broken script must be rejected")
	}
}
