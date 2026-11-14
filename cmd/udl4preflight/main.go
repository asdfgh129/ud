// Command udl4preflight inventories the prerequisites for the WBS 11 L4
// real-client matrix (docs/12 §3.1). It never executes a case and never
// changes the release-gate verdict; see internal/interop/preflight.go.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/asdfgh129/ud/internal/interop"
)

func main() {
	out := flag.String("out", "dist/l4_preflight.json", "machine-readable inventory path")
	md := flag.String("md", "dist/L4_PREFLIGHT.md", "human-readable inventory path")
	cfg := flag.String("config", os.Getenv("UD_L4_CONFIG"),
		"external harness config to inspect (see docs/12 §3.1.3)")
	requireReady := flag.Bool("require-ready", false,
		"exit non-zero if any tool-verifiable prerequisite is not PRESENT")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: udl4preflight [flags]\n\nL4 lab prerequisite inventory (NOT an L4 run).\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	rep := interop.CheckLabPrerequisites(interop.PreflightOptions{ExternalConfig: *cfg})
	if err := rep.WritePreflightEvidence(*out, *md); err != nil {
		fmt.Fprintln(os.Stderr, "udl4preflight:", err)
		os.Exit(2)
	}
	fmt.Printf("L4 preflight: ready=%v | PRESENT %d / MISSING %d / UNVERIFIED %d\n",
		rep.Ready, rep.PresentCount, rep.MissingCount, rep.UnverifiedCount)
	fmt.Printf("  l4_executed=%v release_gate_met=%v (hard invariants)\n",
		rep.L4Executed, rep.ReleaseGateMet)
	fmt.Printf("  manual sign-off required: %v\n", rep.ManualSignoff)
	fmt.Printf("  evidence: %s, %s\n", *out, *md)
	if *requireReady && !rep.Ready {
		fmt.Fprintln(os.Stderr, "udl4preflight: lab prerequisites not ready; do NOT start the L4 matrix")
		os.Exit(1)
	}
}
