package interop

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var auditNow = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

// syntheticReport builds an honest "L4 not executed" bundle: every gate case
// is SKIPPED / UNSUPPORTED, local loopback cases are PASS with evidence.
func syntheticReport() Report {
	rep := Report{
		Schema:            schemaVersion,
		GeneratedAt:       "2026-11-14T12:00:00Z",
		RequireExternal:   true,
		ReleaseGateMet:    false,
		ReleaseGateReason: "L4 发布门禁未通过（SKIPPED / UNSUPPORTED 不计为 PASS）",
	}
	for _, c := range Registry() {
		r := Result{
			ID: c.ID, Title: c.Title, Criterion: c.Criterion,
			Scope: c.Scope, Gate: c.Gate, Reason: "not executed in this bundle",
		}
		if !c.Gate {
			r.Status = StatusPass
			r.Reason = "loopback regression evidence"
			r.Evidence = Evidence{"loopback": true}
		} else if c.Scope == ScopeProductGap {
			r.Status = StatusUnsupported
		} else {
			r.Status = StatusSkipped
		}
		rep.Results = append(rep.Results, r)
	}
	rep.Summary = recomputeForTest(rep)
	return rep
}

func recomputeForTest(rep Report) Summary {
	var s Summary
	for _, r := range rep.Results {
		s.Total++
		if r.Gate {
			s.GateTotal++
			if r.Status == StatusPass {
				s.GatePass++
			}
		}
		switch r.Status {
		case StatusPass:
			s.Pass++
		case StatusFail:
			s.Fail++
		case StatusSkipped:
			s.Skipped++
		case StatusUnsupported:
			s.Unsupported++
		}
	}
	return s
}

func auditOf(t *testing.T, rep Report) (EvidenceAudit, []byte) {
	t.Helper()
	blob, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	return AuditEvidence(blob, "dist/l4_lab/interop.json", auditNow), blob
}

func failedChecks(a EvidenceAudit) []string {
	var out []string
	for _, c := range a.Checks {
		if !c.OK {
			out = append(out, c.ID)
		}
	}
	return out
}

func hasCheck(a EvidenceAudit, id string, ok bool) bool {
	for _, c := range a.Checks {
		if c.ID == id && c.OK == ok {
			return true
		}
	}
	return false
}

func TestAuditEvidenceConsistentUnmetBundle(t *testing.T) {
	a, _ := auditOf(t, syntheticReport())
	if a.Verdict != AuditConsistent {
		t.Fatalf("verdict = %s, failed = %v", a.Verdict, failedChecks(a))
	}
	if a.L4ExecutedByAudit {
		t.Fatal("auditor must never claim it executed L4")
	}
	if a.RecomputedReleaseGateMet {
		t.Fatal("SKIPPED / UNSUPPORTED must not recompute into a met gate")
	}
	if a.GateCases != 14 || a.GatePass != 0 {
		t.Fatalf("gate cases = %d, gate pass = %d; want 14 / 0", a.GateCases, a.GatePass)
	}
	if len(a.GateNonPass) != 14 {
		t.Fatalf("gate_non_pass = %d, want 14", len(a.GateNonPass))
	}
	if a.ChecksFailed != 0 || a.ChecksPassed != len(a.Checks) {
		t.Fatalf("checks passed/failed = %d/%d", a.ChecksPassed, a.ChecksFailed)
	}
	for _, id := range []string{
		"AUD-SCHEMA", "AUD-GATE-COVERAGE", "AUD-STATUS-ENUM", "AUD-SUMMARY",
		"AUD-GATE-VERDICT", "AUD-GATE-REASON", "AUD-EXTERNAL-FLAG",
		"AUD-PASS-EVIDENCE", "AUD-SUPPLEMENTAL",
	} {
		if !hasCheck(a, id, true) {
			t.Errorf("missing passing check %s", id)
		}
	}
}

func TestAuditEvidenceDetectsFlippedGateVerdict(t *testing.T) {
	rep := syntheticReport()
	rep.ReleaseGateMet = true // hand-edited claim, counter to the case list
	a, _ := auditOf(t, rep)
	if a.Verdict != AuditInconsistent || !hasCheck(a, "AUD-GATE-VERDICT", false) {
		t.Fatalf("tampered release_gate_met not detected: %s / %v", a.Verdict, failedChecks(a))
	}
	if a.RecomputedReleaseGateMet {
		t.Fatal("recomputed verdict must stay false")
	}
}

func TestAuditEvidenceDetectsSummaryLaundering(t *testing.T) {
	rep := syntheticReport()
	rep.Summary.Pass = 99
	a, _ := auditOf(t, rep)
	if a.Verdict != AuditInconsistent || !hasCheck(a, "AUD-SUMMARY", false) {
		t.Fatalf("summary laundering not detected: %v", failedChecks(a))
	}
}

func TestAuditEvidenceDetectsMissingAndDuplicateGateCases(t *testing.T) {
	rep := syntheticReport()
	var kept []Result
	for _, r := range rep.Results {
		if r.ID != "BT-14" {
			kept = append(kept, r)
		}
	}
	rep.Results = kept
	rep.Summary = recomputeForTest(rep)
	a, _ := auditOf(t, rep)
	if a.Verdict != AuditInconsistent || !hasCheck(a, "AUD-GATE-COVERAGE", false) {
		t.Fatalf("missing BT-14 not detected: %v", failedChecks(a))
	}

	rep2 := syntheticReport()
	dup := rep2.Results[0] // BT-01
	rep2.Results = append(rep2.Results, dup)
	rep2.Summary = recomputeForTest(rep2)
	a2, _ := auditOf(t, rep2)
	if a2.Verdict != AuditInconsistent || !hasCheck(a2, "AUD-GATE-COVERAGE", false) {
		t.Fatalf("duplicate gate case not detected: %v", failedChecks(a2))
	}
}

func TestAuditEvidenceRejectsUnknownStatus(t *testing.T) {
	rep := syntheticReport()
	for i := range rep.Results {
		if rep.Results[i].ID == "BT-01" {
			rep.Results[i].Status = Status("MAYBE")
		}
	}
	rep.Summary = recomputeForTest(rep)
	a, _ := auditOf(t, rep)
	if a.Verdict != AuditInconsistent || !hasCheck(a, "AUD-STATUS-ENUM", false) {
		t.Fatalf("unknown status not detected: %v", failedChecks(a))
	}
}

func TestAuditEvidenceAllPassRequiresEvidenceAndExternalFlag(t *testing.T) {
	rep := syntheticReport()
	rep.RequireExternal = false
	for i := range rep.Results {
		if rep.Results[i].Gate {
			rep.Results[i].Status = StatusPass
			rep.Results[i].Reason = "real harness verdict"
			rep.Results[i].Evidence = nil
		}
	}
	rep.Summary = recomputeForTest(rep)
	rep.ReleaseGateMet = true
	rep.ReleaseGateReason = "BT-01..BT-14 全部 PASS"
	a, _ := auditOf(t, rep)
	if a.Verdict != AuditInconsistent {
		t.Fatalf("expected INCONSISTENT, got %s", a.Verdict)
	}
	if !hasCheck(a, "AUD-PASS-EVIDENCE", false) || !hasCheck(a, "AUD-EXTERNAL-FLAG", false) {
		t.Fatalf("missing evidence / external flag not detected: %v", failedChecks(a))
	}

	// With evidence and the external harness enabled, the bundle is internally
	// consistent — but the auditor STILL never says it executed L4.
	rep.RequireExternal = true
	for i := range rep.Results {
		if rep.Results[i].Gate {
			rep.Results[i].Evidence = Evidence{"pcap": "capture.pcap", "sha256": "ab"}
		}
	}
	a2, _ := auditOf(t, rep)
	if a2.Verdict != AuditConsistent {
		t.Fatalf("expected CONSISTENT, got %s / %v", a2.Verdict, failedChecks(a2))
	}
	if !a2.RecomputedReleaseGateMet {
		t.Fatal("14/14 PASS should recompute into a met gate")
	}
	if a2.L4ExecutedByAudit {
		t.Fatal("auditor must never claim L4 execution, even for an all-PASS bundle")
	}
	if !strings.Contains(a2.Note, "不等于 L4 矩阵已执行") {
		t.Fatal("integrity-only note lost")
	}
}

func TestAuditEvidenceDetectsFalseFullPassReason(t *testing.T) {
	rep := syntheticReport()
	rep.ReleaseGateReason = "BT-01..BT-14 全部 PASS"
	a, _ := auditOf(t, rep)
	if a.Verdict != AuditInconsistent || !hasCheck(a, "AUD-GATE-REASON", false) {
		t.Fatalf("false full-pass reason not detected: %v", failedChecks(a))
	}
}

func TestAuditEvidenceDetectsBadSupplementalStatus(t *testing.T) {
	rep := syntheticReport()
	rep.ExternalEvidence = []ExternalVerdict{{ID: "BX-01", Client: "real", Status: "MAYBE"}}
	a, _ := auditOf(t, rep)
	if a.Verdict != AuditInconsistent || !hasCheck(a, "AUD-SUPPLEMENTAL", false) {
		t.Fatalf("bad supplemental status not detected: %v", failedChecks(a))
	}
}

func TestAuditEvidenceMalformedAndMissing(t *testing.T) {
	a := AuditEvidence([]byte("{not json}"), "bad.json", auditNow)
	if a.Verdict != AuditInconsistent || !hasCheck(a, "AUD-PARSE", false) {
		t.Fatalf("malformed JSON not reported: %s", a.Verdict)
	}
	m := MissingEvidenceAudit("dist/l4_lab/interop.json", "no such file", auditNow)
	if m.Verdict != AuditMissing || m.ChecksFailed != 1 || m.L4ExecutedByAudit {
		t.Fatalf("missing audit incorrect: %+v", m)
	}
}

func TestAuditEvidenceIsPure(t *testing.T) {
	_, blob := auditOf(t, syntheticReport())
	a1 := AuditEvidence(blob, "x.json", auditNow)
	a2 := AuditEvidence(blob, "x.json", auditNow)
	b1, _ := json.Marshal(a1)
	b2, _ := json.Marshal(a2)
	if string(b1) != string(b2) {
		t.Fatal("audit is not deterministic for identical input")
	}
}

func TestRenderAuditMarkdownIsIntegrityOnly(t *testing.T) {
	a, _ := auditOf(t, syntheticReport())
	md := RenderAuditMarkdown(a)
	for _, want := range []string{
		"# L4 证据包完整性审计报告", "CONSISTENT", "不是 L4 测试结果",
		"l4_executed_by_audit=false", "不接入 CI", "AUD-GATE-VERDICT",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
	if strings.Contains(md, "L4 已通过") {
		t.Fatal("markdown must not claim L4 passed")
	}
}
