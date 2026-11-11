// Command udinterop runs the WBS 11 interoperability suite v1 and writes
// machine-readable evidence into dist/.
//
// It deliberately does not report "P1 released". The L4 release gate needs
// real third-party clients, packet capture and a gigabit LAN; when those are
// absent the corresponding cases are SKIPPED and the report says so.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/asdfgh129/ud/internal/interop"
)

func main() {
	var (
		outDir          = flag.String("out", "dist", "directory for interop.json / INTEROP.md")
		requireExternal = flag.Bool("require-external", envBool("UD_INTEROP_EXTERNAL"),
			"enable the external-client harness and enforce the L4 release gate")
		externalConfig = flag.String("external-config", os.Getenv("UD_INTEROP_CONFIG"),
			"JSON file mapping case IDs to external harness commands")
		caseTimeout     = flag.Duration("case-timeout", interop.DefaultCaseTimeout, "per-case timeout")
		list            = flag.Bool("list", false, "print the case matrix and exit")
		releaseGateOnly = flag.Bool("release-gate-only", false,
			"exit non-zero unless every L4 gate case is PASS")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: udinterop [flags]\n\nWBS 11 interoperability suite v1 (docs/12 §3.1).\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *list {
		printMatrix()
		return
	}

	o := interop.Options{
		RequireExternal: *requireExternal,
		ExternalConfig:  *externalConfig,
		CaseTimeout:     *caseTimeout,
		OutDir:          *outDir,
	}
	report, err := interop.RunAll(context.Background(), o)
	printSummary(report, *outDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "interop: %v\n", err)
		os.Exit(2)
	}
	if report.Summary.Fail > 0 {
		fmt.Fprintf(os.Stderr, "interop: %d case(s) FAILED\n", report.Summary.Fail)
		os.Exit(1)
	}
	if *requireExternal || *releaseGateOnly {
		if !report.ReleaseGateMet {
			fmt.Fprintf(os.Stderr, "interop: L4 release gate NOT met: %s\n", report.ReleaseGateReason)
			os.Exit(1)
		}
	}
}

func printMatrix() {
	cases := interop.Registry()
	keys := []string{"编号", "门禁", "Scope", "用例"}
	fmt.Println(strings.Join(keys, "\t"))
	for _, c := range cases {
		gate := "L3"
		if c.Gate {
			gate = "L4"
		}
		fmt.Printf("%s\t%s\t%s\t%s\n", c.ID, gate, c.Scope, c.Title)
	}
}

func printSummary(r interop.Report, outDir string) {
	fmt.Printf("WBS 11 interop suite v1  %s\n", r.GeneratedAt)
	fmt.Printf("  cases: %d | L4 gate PASS: %d/%d | PASS %d / FAIL %d / SKIPPED %d / UNSUPPORTED %d\n",
		r.Summary.Total, r.Summary.GatePass, r.Summary.GateTotal,
		r.Summary.Pass, r.Summary.Fail, r.Summary.Skipped, r.Summary.Unsupported)
	for _, x := range r.Results {
		if x.Status == interop.StatusFail {
			fmt.Printf("  [FAIL] %s: %s\n", x.ID, x.Reason)
		}
	}
	fmt.Printf("  release gate: %v (%s)\n", r.ReleaseGateMet, r.ReleaseGateReason)
	fmt.Printf("  evidence: %s/interop.json, %s/INTEROP.md\n", outDir, outDir)
}

func envBool(name string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return v == "1" || v == "true" || v == "yes"
}
