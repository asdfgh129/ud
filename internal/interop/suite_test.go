package interop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRegistryIsComplete(t *testing.T) {
	cases := Registry()
	if len(cases) != 23 {
		t.Fatalf("registry size = %d, want 23 (14 L4 + 9 L3)", len(cases))
	}
	seen := map[string]bool{}
	gates := 0
	for i, c := range cases {
		if seen[c.ID] {
			t.Fatalf("duplicate case id %q", c.ID)
		}
		seen[c.ID] = true
		if c.ID == "" || c.Title == "" || c.Criterion == "" || c.Scope == "" {
			t.Fatalf("case %d has empty metadata: %+v", i, c)
		}
		if c.Gate {
			gates++
			want := fmt.Sprintf("BT-%02d", gates)
			if c.ID != want {
				t.Fatalf("gate case %d = %q, want %q", gates, c.ID, want)
			}
			if c.Run == nil || c.Requires == "" {
				t.Fatalf("gate case %s must be runnable and document its prerequisites", c.ID)
			}
		} else if !strings.HasPrefix(c.ID, "L3-") {
			t.Fatalf("unexpected local case id %q", c.ID)
		}
	}
	if gates != 14 {
		t.Fatalf("gate cases = %d, want 14", gates)
	}
}

func TestRunAllProducesHonestEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping loopback integration suite in -short mode")
	}
	dir := t.TempDir()
	rep, err := RunAll(context.Background(), Options{OutDir: dir, CaseTimeout: 90 * time.Second})
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}
	if rep.Schema != schemaVersion {
		t.Fatalf("schema = %q", rep.Schema)
	}
	if rep.Summary.Fail != 0 {
		for _, r := range rep.Results {
			if r.Status == StatusFail {
				t.Errorf("local case failed: %s: %s", r.ID, r.Reason)
			}
		}
		t.Fatalf("%d case(s) FAILED", rep.Summary.Fail)
	}
	if rep.Summary.GatePass != 0 || rep.ReleaseGateMet {
		t.Fatal("L4 gate must NOT be met without real external clients")
	}
	if rep.Summary.Unsupported != 2 || rep.Summary.Skipped != 12 {
		t.Fatalf("unexpected skips: %+v", rep.Summary)
	}
	localPass := 0
	for _, r := range rep.Results {
		if !r.Gate {
			if r.Status != StatusPass {
				t.Errorf("local case %s = %s: %s", r.ID, r.Status, r.Reason)
			} else {
				localPass++
			}
		}
	}
	if localPass != 9 {
		t.Fatalf("local PASS = %d, want 9", localPass)
	}

	for _, name := range []string{"interop.json", "INTEROP.md"} {
		p := filepath.Join(dir, name)
		info, err := os.Stat(p)
		if err != nil || info.Size() == 0 {
			t.Fatalf("missing or empty evidence %s: %v", name, err)
		}
	}
	loaded, err := LoadReport(filepath.Join(dir, "interop.json"))
	if err != nil {
		t.Fatalf("LoadReport: %v", err)
	}
	if loaded.Summary.Total != rep.Summary.Total || loaded.ReleaseGateMet != rep.ReleaseGateMet {
		t.Fatal("round-tripped report differs from in-memory report")
	}
	md := rep.Markdown()
	for _, want := range []string{"L4 发布门禁", "SKIPPED", "NFR-PERF-01", "BT-01"} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown missing %q", want)
		}
	}
}

func TestReportMarkdownEscaping(t *testing.T) {
	rep := Report{
		Schema: schemaVersion, GeneratedAt: "2026-11-11T00:00:00Z",
		Summary:           Summary{Total: 1, GateTotal: 1},
		ReleaseGateMet:    true,
		ReleaseGateReason: "all pass",
		Results: []Result{{
			ID: "X-1", Title: "t", Scope: ScopeInProcess, Gate: true,
			Status: StatusPass, Reason: "a|b\nc", Evidence: Evidence{"z": 1, "a": "two"},
		}},
	}
	md := rep.Markdown()
	if !strings.Contains(md, `a\|b c`) {
		t.Fatalf("table cell not escaped: %s", md)
	}
	if !strings.Contains(md, "✅ 通过") {
		t.Fatal("missing release verdict")
	}
	if !strings.Contains(md, `"a": "two"`) || !strings.Contains(md, `"z": 1`) {
		t.Fatal("evidence JSON not rendered with sorted keys")
	}
	if got := releaseVerdict(false); !strings.Contains(got, "未通过") {
		t.Fatalf("releaseVerdict(false) = %q", got)
	}
	if got := escapeCell("x\ny|z"); got != "x y\\|z" {
		t.Fatalf("escapeCell = %q", got)
	}
	if got := tailString("0123456789", 4); got != "...6789" {
		t.Fatalf("tailString = %q", got)
	}
	if got := tailString("abc", 4); got != "abc" {
		t.Fatalf("tailString short = %q", got)
	}
}

// TestLoadExternalEvidenceStatuses guards the honest-status contract: an
// optional external client that is unavailable is UNSUPPORTED, never PASS, and
// must not invalidate the whole external evidence file.
func TestLoadExternalEvidenceStatuses(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "interop_external.json")
	valid := `[
		{"id":"BX-01","client":"real A","status":"PASS","name":"hash ok","evidence":"sha256=ab","infohash":"ab"},
		{"id":"BX-02","client":"real B","status":"SKIPPED","name":"not run","evidence":"binary missing"},
		{"id":"BX-03","client":"real C","status":"UNSUPPORTED","name":"binding absent","evidence":"python libtorrent binding unavailable"},
		{"id":"BX-04","client":"real D","status":"FAIL","name":"hash mismatch","evidence":"mismatch","error":"hash mismatch"}
	]`
	if err := os.WriteFile(p, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	vs, err := loadExternalEvidence(p)
	if err != nil || len(vs) != 4 {
		t.Fatalf("loadExternalEvidence = %d entries, err=%v", len(vs), err)
	}
	rep := Report{Schema: schemaVersion, Summary: Summary{Total: 23, GateTotal: 14}, ExternalEvidence: vs}
	md := rep.Markdown()
	for _, want := range []string{"UNSUPPORTED", "绝不等于通过", "BX-03", "libtorrent 2.x"} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown missing %q", want)
		}
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`[{"id":"BX-9","status":"MAYBE"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadExternalEvidence(bad); err == nil {
		t.Fatal("expected error for unknown status")
	}
}

func TestWriteEvidenceErrors(t *testing.T) {
	rep := Report{Schema: schemaVersion}
	// A regular file cannot be used as a directory.
	f := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rep.WriteEvidence(filepath.Join(f, "child")); err == nil {
		t.Fatal("expected error writing evidence into a file path")
	}
	if _, err := LoadReport(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("expected error for missing report")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReport(bad); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseVerdict(t *testing.T) {
	v, err := parseVerdict([]byte("noise\n" + `{"status":"PASS","reason":"ok"}` + "\n"))
	if err != nil || v.Status != StatusPass || v.Reason != "ok" {
		t.Fatalf("valid verdict: %+v %v", v, err)
	}
	if _, err := parseVerdict([]byte("no json here")); err == nil {
		t.Fatal("expected error for missing verdict")
	}
	if _, err := parseVerdict([]byte("{not-json}")); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if _, err := parseVerdict([]byte(`{"status":"MAYBE"}`)); err == nil {
		t.Fatal("expected error for unknown status")
	}
	for _, s := range []Status{StatusFail, StatusSkipped, StatusUnsupported} {
		v, err := parseVerdict([]byte(fmt.Sprintf(`{"status":%q}`, s)))
		if err != nil || v.Status != s {
			t.Fatalf("status %s: %+v %v", s, v, err)
		}
	}
}

func TestLoadExternalConfig(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "cfg.json")
	blob := `{"cases":{"BT-01":{"command":["echo","hi"],"timeout_s":30,"note":"lab"}}}`
	if err := os.WriteFile(good, []byte(blob), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadExternalConfig(good)
	if err != nil || len(cfg.Cases) != 1 || cfg.Cases["BT-01"].TimeoutS != 30 {
		t.Fatalf("good config: %+v %v", cfg, err)
	}
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(`{"cases":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadExternalConfig(empty); err == nil {
		t.Fatal("expected error for empty cases")
	}
	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadExternalConfig(broken); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if _, err := loadExternalConfig(filepath.Join(dir, "absent.json")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

// TestExternalHarnessHelper is executed as a child process by
// TestExternalHarness; it is a no-op in the normal test run.
func TestExternalHarnessHelper(t *testing.T) {
	if os.Getenv("UD_INTEROP_HELPER") == "" {
		return
	}
	fmt.Println(`{"status":"PASS","reason":"external lab stub verdict","evidence":{"helper":true}}`)
}

func TestExternalHarness(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "external.json")
	cfg := map[string]any{
		"cases": map[string]any{
			"BT-01": map[string]any{
				"command":   []string{os.Args[0], "-test.run=^TestExternalHarnessHelper$"},
				"timeout_s": 30,
			},
		},
	}
	blob, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UD_INTEROP_HELPER", "1")
	rep, err := RunAll(context.Background(), Options{
		RequireExternal: true, ExternalConfig: cfgPath, CaseTimeout: 90 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	bt01 := findResult(rep, "BT-01")
	if bt01 == nil || bt01.Status != StatusPass {
		t.Fatalf("BT-01 external harness verdict = %+v", bt01)
	}
	if !strings.Contains(bt01.Reason, "stub verdict") {
		t.Fatalf("verdict reason lost: %q", bt01.Reason)
	}
	// A case with no configured command must stay SKIPPED.
	if got := findResult(rep, "BT-03"); got == nil || got.Status != StatusSkipped ||
		!strings.Contains(got.Reason, "未包含该用例") {
		t.Fatalf("BT-03 should be skipped: %+v", got)
	}

	// Enabling the harness without a config is a configuration error, not a skip.
	rep2, err := RunAll(context.Background(), Options{RequireExternal: true, CaseTimeout: 90 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if got := findResult(rep2, "BT-01"); got == nil || got.Status != StatusFail {
		t.Fatalf("missing config must FAIL: %+v", got)
	}
}

func TestRunSafelyGuards(t *testing.T) {
	withID := withCaseID(context.Background(), "X-9")
	if caseIDFromContext(withID) != "X-9" || caseIDFromContext(context.Background()) != "" {
		t.Fatal("case id context helpers broken")
	}

	panicking := Case{ID: "P", Run: func(context.Context, Options) Outcome { panic("boom") }}
	if out := runSafely(context.Background(), panicking, Options{}); out.Status != StatusFail ||
		!strings.Contains(out.Reason, "panicked") {
		t.Fatalf("panic not contained: %+v", out)
	}
	empty := Case{ID: "E", Run: func(context.Context, Options) Outcome { return Outcome{} }}
	if out := runSafely(context.Background(), empty, Options{}); out.Status != StatusFail {
		t.Fatalf("empty status not rejected: %+v", out)
	}
	blocked := Case{ID: "B", Run: func(ctx context.Context, _ Options) Outcome {
		<-ctx.Done()
		return pass("unexpected", nil)
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if out := runSafely(ctx, blocked, Options{}); out.Status != StatusFail ||
		!strings.Contains(out.Reason, "timeout") {
		t.Fatalf("timeout not detected: %+v", out)
	}
}

func findResult(r Report, id string) *Result {
	for i := range r.Results {
		if r.Results[i].ID == id {
			return &r.Results[i]
		}
	}
	return nil
}
