// L4 evidence-bundle integrity audit (docs/18 §7.3 / §7.4).
//
// This is deliberately NOT an interoperability test and NOT a release gate.
// It reads an already-produced ud.interop/v1 report and independently
// recomputes its counters and release verdict from the raw case list. Its only
// purpose is to detect a bundle whose summary, gate verdict or gate coverage
// disagrees with its own cases (accidental drift or hand-editing).
//
// Hard invariants:
//   - it never runs a case, never spawns a process and never touches the net;
//   - a CONSISTENT verdict means "internally consistent", NOT "L4 passed";
//   - it never sets release_gate_met; RecomputedReleaseGateMet is a factual
//     recomputation of the INPUT document, not an assertion by the auditor;
//   - L4ExecutedByAudit is always false;
//   - SKIPPED / UNSUPPORTED / PRESENT are never coerced into PASS.
package interop

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const evidenceAuditSchema = "ud.l4-evidence-audit/v1"

// AuditVerdict is an integrity verdict, NOT an L4 test result.
type AuditVerdict string

const (
	AuditConsistent   AuditVerdict = "CONSISTENT"
	AuditInconsistent AuditVerdict = "INCONSISTENT"
	AuditMissing      AuditVerdict = "MISSING"
)

// AuditCheck is one deterministic integrity check.
type AuditCheck struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// EvidenceAudit is the machine-readable audit report.
type EvidenceAudit struct {
	Schema                   string       `json:"schema"`
	GeneratedAt              string       `json:"generated_at"`
	Scope                    string       `json:"scope"`
	Input                    string       `json:"input"`
	Verdict                  AuditVerdict `json:"verdict"`
	Checks                   []AuditCheck `json:"checks"`
	ChecksPassed             int          `json:"checks_passed"`
	ChecksFailed             int          `json:"checks_failed"`
	RecomputedSummary        Summary      `json:"recomputed_summary"`
	RecomputedReleaseGateMet bool         `json:"recomputed_release_gate_met"`
	GateCases                int          `json:"gate_cases"`
	GatePass                 int          `json:"gate_pass"`
	GateNonPass              []string     `json:"gate_non_pass"`
	L4ExecutedByAudit        bool         `json:"l4_executed_by_audit"`
	Note                     string       `json:"note"`
}

const auditNote = "CONSISTENT 仅表示证据包内部自洽，不等于 L4 矩阵已执行或通过；" +
	"INCONSISTENT / MISSING 绝不表示 L4 通过。发布门禁只由被审计文档的 " +
	"case 列表重算，审计器本身不设置 release_gate_met、不产出任何 L4 证据。" +
	"SKIPPED / UNSUPPORTED / PRESENT 一律不计 PASS。"

func newAudit(input string, now time.Time) EvidenceAudit {
	return EvidenceAudit{
		Schema:            evidenceAuditSchema,
		GeneratedAt:       now.UTC().Format(time.RFC3339),
		Scope:             "L4 证据包完整性审计（integrity only；NOT an L4 execution, NOT a release gate）",
		Input:             input,
		GateNonPass:       []string{},
		L4ExecutedByAudit: false,
		Note:              auditNote,
	}
}

// MissingEvidenceAudit reports that no evidence bundle could be read. It is a
// MISSING inventory, never an empty/fabricated PASS bundle.
func MissingEvidenceAudit(input, reason string, now time.Time) EvidenceAudit {
	a := newAudit(input, now)
	a.Verdict = AuditMissing
	a.Checks = []AuditCheck{{ID: "AUD-INPUT", OK: false,
		Detail: fmt.Sprintf("证据包不可读：%s；未执行任何 L4 用例", reason)}}
	a.ChecksFailed = 1
	return a
}

func canonicalGateIDs() []string {
	var ids []string
	for _, c := range Registry() {
		if c.Gate {
			ids = append(ids, c.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func validStatus(s Status) bool {
	switch s {
	case StatusPass, StatusFail, StatusSkipped, StatusUnsupported:
		return true
	}
	return false
}

// AuditEvidence recomputes an evidence bundle's integrity. It is pure: no I/O,
// no process spawning, no network access and no mutation of the input report.
func AuditEvidence(blob []byte, input string, now time.Time) EvidenceAudit {
	a := newAudit(input, now)
	var rep Report
	if err := json.Unmarshal(blob, &rep); err != nil {
		a.Verdict = AuditInconsistent
		a.Checks = []AuditCheck{{ID: "AUD-PARSE", OK: false,
			Detail: "输入不是合法 ud.interop/v1 JSON：" + err.Error()}}
		a.ChecksFailed = 1
		return a
	}

	add := func(id string, ok bool, format string, args ...any) {
		a.Checks = append(a.Checks, AuditCheck{ID: id, OK: ok, Detail: fmt.Sprintf(format, args...)})
	}

	// 1. Schema identity.
	add("AUD-SCHEMA", rep.Schema == schemaVersion,
		"schema=%q，期望 %q", rep.Schema, schemaVersion)

	// 2. Gate coverage: exactly the canonical BT-01..BT-14, once each, and no
	//    non-canonical case marked as a gate case.
	want := canonicalGateIDs()
	wantSet := make(map[string]bool, len(want))
	for _, id := range want {
		wantSet[id] = true
	}
	seen := map[string]int{}
	var gateResults []Result
	for _, r := range rep.Results {
		if r.Gate {
			seen[r.ID]++
			gateResults = append(gateResults, r)
		}
	}
	var missing, extra, duplicated []string
	for _, id := range want {
		if seen[id] == 0 {
			missing = append(missing, id)
		} else if seen[id] > 1 {
			duplicated = append(duplicated, fmt.Sprintf("%s×%d", id, seen[id]))
		}
	}
	for id := range seen {
		if !wantSet[id] {
			extra = append(extra, id)
		}
	}
	sort.Strings(extra)
	coverageOK := len(missing) == 0 && len(extra) == 0 && len(duplicated) == 0 && len(want) == 14
	add("AUD-GATE-COVERAGE", coverageOK,
		"规范门禁 %d 条；缺 %v / 多 %v / 重复 %v", len(want), missing, extra, duplicated)

	// 3. Status enum + basic record hygiene for every case.
	var badStatus, blankID, blankReason []string
	for _, r := range rep.Results {
		if !validStatus(r.Status) {
			badStatus = append(badStatus, fmt.Sprintf("%s=%s", r.ID, r.Status))
		}
		if strings.TrimSpace(r.ID) == "" {
			blankID = append(blankID, "empty-id")
		}
		if strings.TrimSpace(r.Reason) == "" {
			blankReason = append(blankReason, r.ID)
		}
	}
	add("AUD-STATUS-ENUM", len(badStatus) == 0 && len(blankID) == 0 && len(blankReason) == 0,
		"非法 status=%v / 空 ID=%v / 空 reason=%v", badStatus, blankID, blankReason)

	// 4. Recompute every counter from the raw case list.
	var recomputed Summary
	for _, r := range rep.Results {
		recomputed.Total++
		if r.Gate {
			recomputed.GateTotal++
			if r.Status == StatusPass {
				recomputed.GatePass++
			}
		}
		switch r.Status {
		case StatusPass:
			recomputed.Pass++
		case StatusFail:
			recomputed.Fail++
		case StatusSkipped:
			recomputed.Skipped++
		case StatusUnsupported:
			recomputed.Unsupported++
		}
	}
	a.RecomputedSummary = recomputed
	a.GateCases = len(gateResults)
	a.GatePass = recomputed.GatePass
	for _, r := range gateResults {
		if r.Status != StatusPass {
			a.GateNonPass = append(a.GateNonPass, fmt.Sprintf("%s=%s", r.ID, r.Status))
		}
	}
	sort.Strings(a.GateNonPass)
	add("AUD-SUMMARY", recomputed == rep.Summary,
		"重算 %+v；文档 %+v", recomputed, rep.Summary)

	// 5. Release verdict must be a pure function of the case list.
	allGatesPass := len(want) > 0 && len(gateResults) == len(want) &&
		recomputed.GatePass == len(want)
	expectedMet := recomputed.Fail == 0 && len(badStatus) == 0 && allGatesPass
	a.RecomputedReleaseGateMet = expectedMet
	add("AUD-GATE-VERDICT", rep.ReleaseGateMet == expectedMet,
		"文档 release_gate_met=%v；按 case 重算=%v（gate_pass=%d/%d，FAIL=%d）",
		rep.ReleaseGateMet, expectedMet, recomputed.GatePass, len(want), recomputed.Fail)

	// 6. A reason string must not claim a full matrix pass unless it is true.
	claimedFullPass := strings.Contains(rep.ReleaseGateReason, "全部 PASS")
	add("AUD-GATE-REASON", !claimedFullPass || allGatesPass,
		"release_gate_reason=%q；all_gates_pass=%v", rep.ReleaseGateReason, allGatesPass)

	// 7. A bundle claiming the L4 gate is met must have been produced with the
	//    external harness enabled; local loopback-only reports cannot claim it.
	add("AUD-EXTERNAL-FLAG", !rep.ReleaseGateMet || rep.RequireExternal,
		"release_gate_met=%v，require_external=%v", rep.ReleaseGateMet, rep.RequireExternal)

	// 8. Every PASS gate case must carry a reason and machine-readable evidence
	//    (docs/18 §7.3 requires raw verdicts, hashes and capture references).
	var passWithoutEvidence []string
	for _, r := range gateResults {
		if r.Status == StatusPass && (strings.TrimSpace(r.Reason) == "" || len(r.Evidence) == 0) {
			passWithoutEvidence = append(passWithoutEvidence, r.ID)
		}
	}
	sort.Strings(passWithoutEvidence)
	add("AUD-PASS-EVIDENCE", len(passWithoutEvidence) == 0,
		"PASS 门禁用例缺 reason/evidence：%v", passWithoutEvidence)

	// 9. Supplemental cross-client evidence is informational only; its statuses
	//    must still be honest and it can never add to the gate pass count.
	var badSupplemental []string
	for _, x := range rep.ExternalEvidence {
		if !validStatus(Status(x.Status)) {
			badSupplemental = append(badSupplemental, fmt.Sprintf("%s=%s", x.ID, x.Status))
		}
	}
	add("AUD-SUPPLEMENTAL", len(badSupplemental) == 0,
		"补充证据非法 status=%v（补充证据永不翻转门禁）", badSupplemental)

	for _, c := range a.Checks {
		if c.OK {
			a.ChecksPassed++
		} else {
			a.ChecksFailed++
		}
	}
	if a.ChecksFailed == 0 {
		a.Verdict = AuditConsistent
	} else {
		a.Verdict = AuditInconsistent
	}
	return a
}

// RenderAuditMarkdown renders a human-readable audit report. It always repeats
// the "integrity-only" warning so a green audit cannot be mistaken for L4.
func RenderAuditMarkdown(a EvidenceAudit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# L4 证据包完整性审计报告\n\n")
	fmt.Fprintf(&b, "> **审计结论：`%s`**（完整性结论，**不是 L4 测试结果、不是发布门禁**）\n>\n", a.Verdict)
	fmt.Fprintf(&b, "> 生成时间：%s ｜ 输入：`%s` ｜ 检查 %d 项（通过 %d / 失败 %d）\n>\n",
		a.GeneratedAt, a.Input, len(a.Checks), a.ChecksPassed, a.ChecksFailed)
	fmt.Fprintf(&b, "> 重算 `gate_pass=%d/%d`，重算 `release_gate_met=%v`，`l4_executed_by_audit=false`。\n>\n",
		a.GatePass, a.GateCases, a.RecomputedReleaseGateMet)
	fmt.Fprintf(&b, "> %s\n\n", auditNote)
	fmt.Fprintf(&b, "| 检查 | 结果 | 说明 |\n|---|---|---|\n")
	for _, c := range a.Checks {
		mark := "OK"
		if !c.OK {
			mark = "**FAIL**"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", c.ID, mark, escapeCell(c.Detail))
	}
	if len(a.GateNonPass) > 0 {
		fmt.Fprintf(&b, "\n未达 PASS 的门禁用例（**保持原状，绝不计为 PASS**）：\n\n")
		for _, x := range a.GateNonPass {
			fmt.Fprintf(&b, "- `%s`\n", x)
		}
	}
	fmt.Fprintf(&b, "\n## 边界\n\n")
	fmt.Fprintf(&b, "1. 本审计**不执行**任何 `BT-*` 用例，不启动外部进程、不访问网络、不写回被审计文档。\n")
	fmt.Fprintf(&b, "2. `CONSISTENT` 只说明汇总与 case 列表自洽；一个自洽的证据包完全可能仍是 `release_gate_met=false`。\n")
	fmt.Fprintf(&b, "3. `MISSING` 表示没有可审计的证据包，**不等于** L4 通过。\n")
	fmt.Fprintf(&b, "4. 审计目标**不接入 CI**；`make interop-gate` 才是发布门禁判定，二者不可互相替代。\n")
	fmt.Fprintf(&b, "5. 真实矩阵、抓包复核与评审签署见 [`18`](../docs/18-序6评审与L4实验室准备材料包.md) §七 / §九。\n")
	return b.String()
}
