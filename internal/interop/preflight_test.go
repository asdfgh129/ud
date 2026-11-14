package interop

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakePreflightOpts() PreflightOptions {
	return PreflightOptions{
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
		Getenv:   func(string) string { return "" },
		ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
		Now:      func() time.Time { return time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC) },
		Euid:     func() int { return 501 },
	}
}

func fullConfigJSON() []byte {
	cases := map[string]externalCase{}
	for _, id := range externalCaseIDs() {
		cases[id] = externalCase{Command: []string{"/opt/l4/harness", "--case", id}}
	}
	blob, _ := json.Marshal(externalConfig{Cases: cases})
	return blob
}

func findReq(r PreflightReport, id string) LabRequirement {
	for _, x := range r.Requirements {
		if x.ID == id {
			return x
		}
	}
	return LabRequirement{}
}

// A preflight is an inventory, never an execution and never a gate verdict.
func TestPreflightHardInvariants(t *testing.T) {
	rep := CheckLabPrerequisites(PreflightOptions{})
	if rep.L4Executed {
		t.Fatal("preflight must never claim the L4 matrix was executed")
	}
	if rep.ReleaseGateMet {
		t.Fatal("preflight must never claim the release gate was met")
	}
	if rep.Schema != preflightSchema || rep.GeneratedAt == "" {
		t.Fatalf("bad schema/timestamp: %+v", rep)
	}
	if len(rep.Requirements) != 9 || len(rep.Runbook) < 5 {
		t.Fatalf("unexpected inventory size: %d requirements / %d runbook steps",
			len(rep.Requirements), len(rep.Runbook))
	}
	if !strings.Contains(rep.ReleaseGateNote, "不等于 PASS") ||
		!strings.Contains(rep.ReviewBlocker, "ADR-0021") {
		t.Fatalf("missing boundary wording: %+v", rep)
	}
	md := rep.Markdown()
	for _, want := range []string{"这不是 L4 执行证据", "绝不等于 PASS", "LAB-CONFIG", "LAB-REVIEW"} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown missing %q", want)
		}
	}
}

func TestPreflightAllPrerequisitesMissing(t *testing.T) {
	rep := CheckLabPrerequisites(fakePreflightOpts())
	if rep.Ready {
		t.Fatal("a stock machine must not be reported as lab-ready")
	}
	if rep.PresentCount != 0 || rep.MissingCount != 8 || rep.UnverifiedCount != 1 {
		t.Fatalf("unexpected counts: %+v", rep)
	}
	for _, id := range []string{"LAB-CLIENT-QB", "LAB-CLIENT-TR", "LAB-TRACKER",
		"LAB-CAPTURE", "LAB-CONFIG", "LAB-TOPOLOGY", "LAB-REVIEW", "LAB-PERF"} {
		if got := findReq(rep, id).Status; got != LabMissing {
			t.Errorf("%s = %s, want MISSING", id, got)
		}
	}
	if got := findReq(rep, "LAB-CLIENT-LT").Status; got != LabUnverified {
		t.Errorf("LAB-CLIENT-LT = %s, want UNVERIFIED", got)
	}
	if len(rep.ManualSignoff) != 4 {
		t.Fatalf("manual sign-off list = %v", rep.ManualSignoff)
	}
}

func TestPreflightReadyMeansOnlyToolVerifiablePrerequisites(t *testing.T) {
	o := fakePreflightOpts()
	o.ExternalConfig = "/opt/l4/config.json"
	o.LookPath = func(name string) (string, error) { return "/opt/bin/" + filepath.Base(name), nil }
	o.Euid = func() int { return 0 }
	o.ReadFile = func(path string) ([]byte, error) {
		if path == o.ExternalConfig {
			return fullConfigJSON(), nil
		}
		return nil, os.ErrNotExist
	}
	rep := CheckLabPrerequisites(o)
	if !rep.Ready {
		t.Fatalf("expected tool-verifiable prerequisites ready: %+v", rep)
	}
	if rep.PresentCount != 5 || rep.MissingCount != 3 || rep.UnverifiedCount != 1 {
		t.Fatalf("unexpected counts: %+v", rep)
	}
	// Human attestation is still outstanding, and the gate stays closed.
	if len(rep.ManualSignoff) != 4 || rep.L4Executed || rep.ReleaseGateMet {
		t.Fatalf("human sign-off / invariants wrong: %+v", rep)
	}
	if got := findReq(rep, "LAB-CONFIG"); got.Status != LabPresent ||
		!strings.Contains(got.Detail, "12 条外部用例") {
		t.Fatalf("config check = %+v", got)
	}
	if got := findReq(rep, "LAB-CAPTURE"); got.Status != LabPresent {
		t.Fatalf("capture check = %+v", got)
	}
	if len(externalCaseIDs()) != 12 {
		t.Fatalf("external case count = %d, want 12", len(externalCaseIDs()))
	}
	for _, id := range externalCaseIDs() {
		if id == "BT-02" || id == "BT-11" {
			t.Fatal("product-gap cases must never be dispatched to an external harness")
		}
	}
}

func TestPreflightRejectsStubAndIncompleteConfig(t *testing.T) {
	o := fakePreflightOpts()
	o.ExternalConfig = "tools/l4_external_config.example.json"
	o.LookPath = func(name string) (string, error) { return "/opt/bin/" + filepath.Base(name), nil }
	o.Euid = func() int { return 0 }

	stubCases := map[string]externalCase{}
	for _, id := range externalCaseIDs() {
		stubCases[id] = externalCase{Command: []string{"python3", "tools/l4_harness_stub.py", id}}
	}
	stubBlob, _ := json.Marshal(externalConfig{Cases: stubCases})
	o.ReadFile = func(string) ([]byte, error) { return stubBlob, nil }
	rep := CheckLabPrerequisites(o)
	if got := findReq(rep, "LAB-CONFIG"); got.Status != LabUnverified ||
		!strings.Contains(got.Detail, "stub") {
		t.Fatalf("stub config must be rejected: %+v", got)
	}
	if rep.Ready {
		t.Fatal("a stub config must never make the lab ready")
	}

	partial, _ := json.Marshal(externalConfig{Cases: map[string]externalCase{
		"BT-01": {Command: []string{"/opt/l4/harness"}},
	}})
	o.ReadFile = func(string) ([]byte, error) { return partial, nil }
	rep = CheckLabPrerequisites(o)
	if got := findReq(rep, "LAB-CONFIG"); got.Status != LabUnverified ||
		!strings.Contains(got.Detail, "1/12") {
		t.Fatalf("incomplete config must be flagged: %+v", got)
	}

	o.ReadFile = func(string) ([]byte, error) { return []byte("{not json"), nil }
	if got := findReq(CheckLabPrerequisites(o), "LAB-CONFIG"); got.Status != LabMissing {
		t.Fatalf("malformed config = %+v", got)
	}
}

func TestPreflightAttestationNeedsHumanVerification(t *testing.T) {
	o := fakePreflightOpts()
	o.Getenv = func(k string) string {
		if k == "UD_L4_TOPOLOGY_ATTESTATION" {
			return "/opt/l4/topo.json"
		}
		return ""
	}
	o.ReadFile = func(string) ([]byte, error) {
		return []byte(`{"peer_host":"10.0.0.2","link_mbps":1000,"signed_by":"lab"}`), nil
	}
	got := findReq(CheckLabPrerequisites(o), "LAB-TOPOLOGY")
	if got.Status != LabUnverified || !strings.Contains(got.Detail, "人工核验") {
		t.Fatalf("self-declared topology must stay UNVERIFIED: %+v", got)
	}

	o.ReadFile = func(string) ([]byte, error) { return []byte(`{"link_mbps":1000}`), nil }
	if got = findReq(CheckLabPrerequisites(o), "LAB-TOPOLOGY"); got.Status != LabUnverified ||
		!strings.Contains(got.Detail, "signed_by") {
		t.Fatalf("missing signer must be flagged: %+v", got)
	}

	o.ReadFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	if got = findReq(CheckLabPrerequisites(o), "LAB-TOPOLOGY"); got.Status != LabMissing {
		t.Fatalf("unreadable attestation = %+v", got)
	}
}

func TestPreflightEnvOverrideAndEvidenceRoundTrip(t *testing.T) {
	o := fakePreflightOpts()
	o.Getenv = func(k string) string {
		if k == "UD_L4_QBITTORRENT" {
			return "/opt/qb/qbittorrent-nox"
		}
		return ""
	}
	o.LookPath = func(p string) (string, error) {
		if p == "/opt/qb/qbittorrent-nox" {
			return p, nil
		}
		return "", errors.New("not found")
	}
	rep := CheckLabPrerequisites(o)
	if got := findReq(rep, "LAB-CLIENT-QB"); got.Status != LabPresent ||
		!strings.Contains(got.Detail, "/opt/qb/qbittorrent-nox") {
		t.Fatalf("env override = %+v", got)
	}

	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "nested", "l4_preflight.json")
	mdPath := filepath.Join(dir, "nested", "L4_PREFLIGHT.md")
	if err := rep.WritePreflightEvidence(jsonPath, mdPath); err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	var back PreflightReport
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatal(err)
	}
	if back.L4Executed || back.ReleaseGateMet || back.Schema != preflightSchema {
		t.Fatalf("round-trip invariants broken: %+v", back)
	}
	if _, err := os.Stat(mdPath); err != nil {
		t.Fatal(err)
	}
}
