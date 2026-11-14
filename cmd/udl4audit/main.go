// Command udl4audit independently audits the integrity of an already-produced
// WBS 11 interop.json evidence bundle (docs/18 §7.3 / §7.4).
//
// It is NOT an L4 test, NOT a release gate and is deliberately not wired into
// CI. A zero exit status means only "the bundle is internally consistent"; a
// consistent bundle may still report release_gate_met=false. It never runs a
// case, never spawns a harness and never writes back to the input document.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/asdfgh129/ud/internal/interop"
)

func main() {
	var (
		in          = flag.String("in", "dist/l4_lab/interop.json", "ud.interop/v1 evidence bundle to audit (read-only)")
		out         = flag.String("out", "dist/l4_audit.json", "audit JSON output (empty disables writing)")
		md          = flag.String("md", "dist/L4_AUDIT.md", "audit Markdown output (empty disables writing)")
		requireGate = flag.Bool("require-gate-met", false,
			"also exit non-zero unless the recomputed release gate is met (integrity + gate check)")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: udl4audit [flags]\n\nL4 evidence-bundle integrity audit (NOT an L4 run, NOT the release gate).\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	now := time.Now()
	var a interop.EvidenceAudit
	blob, err := os.ReadFile(*in)
	if err != nil {
		a = interop.MissingEvidenceAudit(*in, err.Error(), now)
	} else {
		a = interop.AuditEvidence(blob, *in, now)
	}

	if *out != "" {
		if err := writeFile(*out, mustJSON(a)); err != nil {
			fmt.Fprintf(os.Stderr, "udl4audit: write %s: %v\n", *out, err)
			os.Exit(2)
		}
	}
	if *md != "" {
		if err := writeFile(*md, []byte(interop.RenderAuditMarkdown(a))); err != nil {
			fmt.Fprintf(os.Stderr, "udl4audit: write %s: %v\n", *md, err)
			os.Exit(2)
		}
	}

	fmt.Printf("L4 evidence audit: %s | checks %d (OK %d / FAIL %d)\n",
		a.Verdict, len(a.Checks), a.ChecksPassed, a.ChecksFailed)
	fmt.Printf("  recomputed gate_pass=%d/%d | recomputed release_gate_met=%v\n",
		a.GatePass, a.GateCases, a.RecomputedReleaseGateMet)
	fmt.Printf("  l4_executed_by_audit=false | %s\n", a.Input)
	fmt.Println("  NOTE: integrity-only; CONSISTENT is NOT an L4 pass. Release gate is decided by make interop-gate.")
	for _, c := range a.Checks {
		if !c.OK {
			fmt.Printf("  [FAIL] %s: %s\n", c.ID, c.Detail)
		}
	}

	switch {
	case a.Verdict != interop.AuditConsistent:
		os.Exit(1)
	case *requireGate && !a.RecomputedReleaseGateMet:
		fmt.Fprintf(os.Stderr, "udl4audit: recomputed release gate NOT met (%d/%d gate PASS)\n",
			a.GatePass, a.GateCases)
		os.Exit(1)
	}
}

func mustJSON(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		// EvidenceAudit contains only plain data; this cannot fail in practice.
		panic(err)
	}
	return append(b, '\n')
}

func writeFile(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644)
}
