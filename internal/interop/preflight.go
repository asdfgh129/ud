// L4 lab preflight: an inventory of prerequisites for the BT-01..BT-14
// real-client matrix (docs/12 §3.1). It is deliberately NOT an execution of
// the matrix and NEVER flips the release gate.
//
// A green preflight means "the tool-verifiable lab prerequisites are present",
// nothing more. Physical topology, the review signature that resolves the
// ADR-0021 scope conflict, and the two-host NFR-PERF-01 benchmark always
// require human attestation and are listed separately.
package interop

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LabStatus is the outcome of one prerequisite check. There is deliberately
// no PASS verdict here: preflight is an inventory, not a test result.
type LabStatus string

const (
	LabPresent    LabStatus = "PRESENT"
	LabMissing    LabStatus = "MISSING"
	LabUnverified LabStatus = "UNVERIFIED"
)

// LabRequirement is one prerequisite for the external L4 matrix.
type LabRequirement struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Kind             string    `json:"kind"`
	Status           LabStatus `json:"status"`
	Detail           string    `json:"detail"`
	RelatedCases     []string  `json:"related_cases"`
	HumanAttestation bool      `json:"human_attestation"`
}

// PreflightReport is the machine-readable inventory. L4Executed and
// ReleaseGateMet are hard invariants: this tool must never be mistaken for
// an L4 run or for evidence that the release gate was met.
type PreflightReport struct {
	Schema             string           `json:"schema"`
	GeneratedAt        string           `json:"generated_at"`
	Scope              string           `json:"scope"`
	L4Executed         bool             `json:"l4_executed"`
	Ready              bool             `json:"ready"`
	Requirements       []LabRequirement `json:"requirements"`
	PresentCount       int              `json:"present_count"`
	MissingCount       int              `json:"missing_count"`
	UnverifiedCount    int              `json:"unverified_count"`
	ManualSignoff      []string         `json:"manual_signoff"`
	ReleaseGateMet     bool             `json:"release_gate_met"`
	ReleaseGateNote    string           `json:"release_gate_note"`
	ReviewBlocker      string           `json:"review_blocker"`
	Runbook            []string         `json:"runbook"`
	ExternalConfigPath string           `json:"external_config_path,omitempty"`
}

const preflightSchema = "ud.l4-preflight/v1"

// PreflightOptions carries the environment probes. All probes are injectable
// so the inventory logic is fully testable without a real lab.
type PreflightOptions struct {
	ExternalConfig string
	LookPath       func(string) (string, error)
	Getenv         func(string) string
	ReadFile       func(string) ([]byte, error)
	Now            func() time.Time
	Euid           func() int
}

func (o *PreflightOptions) fill() {
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.ReadFile == nil {
		o.ReadFile = os.ReadFile
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Euid == nil {
		o.Euid = os.Geteuid
	}
}

func (o PreflightOptions) firstBinary(envKey string, names ...string) (string, bool) {
	if p := strings.TrimSpace(o.Getenv(envKey)); p != "" {
		if _, err := o.LookPath(p); err == nil {
			return p, true
		}
		return p, false
	}
	for _, n := range names {
		if p, err := o.LookPath(n); err == nil {
			return p, true
		}
	}
	return strings.Join(names, " / "), false
}

// externalCaseIDs lists the gate cases that can actually be dispatched to a
// real external harness. BT-02 / BT-11 are product-gap cases with a fixed
// UNSUPPORTED verdict and must never appear in a config that claims coverage.
func externalCaseIDs() []string {
	var ids []string
	for _, c := range Registry() {
		if c.Gate && c.Scope == ScopeExternal {
			ids = append(ids, c.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// inspectExternalConfig checks that a config is complete and points at real
// commands. The shipped example config uses a stub that can only emit
// SKIPPED, so it is explicitly rejected as "not ready".
func (o PreflightOptions) inspectExternalConfig(path string) (LabStatus, string) {
	if strings.TrimSpace(path) == "" {
		return LabMissing, "未提供 --config / UD_L4_CONFIG；外部 harness 无法执行任何 L4 用例"
	}
	blob, err := o.ReadFile(path)
	if err != nil {
		return LabMissing, fmt.Sprintf("配置不可读：%v", err)
	}
	var cfg externalConfig
	if err := json.Unmarshal(blob, &cfg); err != nil {
		return LabMissing, fmt.Sprintf("配置不是合法 JSON：%v", err)
	}
	want := externalCaseIDs()
	var absent []string
	for _, id := range want {
		if _, ok := cfg.Cases[id]; !ok {
			absent = append(absent, id)
		}
	}
	if len(cfg.Cases) == 0 {
		return LabMissing, "cases 为空"
	}
	if len(absent) > 0 {
		return LabUnverified, fmt.Sprintf("配置只覆盖 %d/%d 条外部用例，缺 %s",
			len(want)-len(absent), len(want), strings.Join(absent, ", "))
	}
	for id, spec := range cfg.Cases {
		if len(spec.Command) == 0 {
			return LabUnverified, id + " 的 command 为空"
		}
		cmd := strings.ToLower(strings.Join(spec.Command, " "))
		if strings.Contains(cmd, "stub") || strings.Contains(cmd, "replace") ||
			strings.Contains(cmd, "example") {
			return LabUnverified, fmt.Sprintf("%s 指向示例 stub（%s）：该 stub 只能输出 SKIPPED，不得据此宣称 L4 通过",
				id, spec.Command[0])
		}
		if _, err := o.LookPath(spec.Command[0]); err != nil {
			return LabUnverified, fmt.Sprintf("%s 的命令 %q 不可执行：%v", id, spec.Command[0], err)
		}
	}
	return LabPresent, fmt.Sprintf("%s：%d 条外部用例均已配置真实命令", path, len(want))
}

func (o PreflightOptions) attestation(envKey, label string) (LabStatus, string) {
	path := strings.TrimSpace(o.Getenv(envKey))
	if path == "" {
		return LabMissing, label + "：未提供签署/实测证据文件（" + envKey + "）"
	}
	blob, err := o.ReadFile(path)
	if err != nil {
		return LabMissing, fmt.Sprintf("%s：证据文件不可读：%v", label, err)
	}
	var v map[string]any
	if err := json.Unmarshal(blob, &v); err != nil {
		return LabUnverified, fmt.Sprintf("%s：证据文件不是合法 JSON：%v", label, err)
	}
	if _, ok := v["signed_by"]; !ok {
		return LabUnverified, label + "：证据文件缺 signed_by，工具无法核验签署"
	}
	return LabUnverified, label + "：已收到自声明文件，但物理链路 / 评审签署 / 实测结果仍须人工核验，工具不代为判定"
}

// CheckLabPrerequisites builds the inventory. It performs no network access,
// starts no external process and writes no gate verdict other than the
// invariant values below.
func CheckLabPrerequisites(in PreflightOptions) PreflightReport {
	o := in
	o.fill()

	rep := PreflightReport{
		Schema:      preflightSchema,
		GeneratedAt: o.Now().UTC().Format(time.RFC3339),
		Scope:       "L4 实验室前置条件清点（NOT an L4 execution; prerequisite inventory only）",
		L4Executed:  false,
		// Hard invariants: a preflight can never satisfy the release gate.
		ReleaseGateMet:     false,
		ReleaseGateNote:    "L4 发布门禁只由 dist/interop.json:release_gate_met 判定；本清单不是执行证据，SKIPPED / UNSUPPORTED / PRESENT 一律不等于 PASS。",
		ReviewBlocker:      "ADR-0021（Proposed，未生效）：实现相对 ADR-0004 / ADR-0017 与 03 的 M 级需求存在未签署范围缩减，评审签署前 14/14 PASS 形式不可满足。",
		ExternalConfigPath: o.ExternalConfig,
		Runbook: []string{
			"1. 在隔离千兆内网准备两台主机，安装真实 qBittorrent / Transmission / libtorrent 系客户端与 chihaya / opentracker 二进制，并确认具备抓包权限。",
			"2. 为每个 BT 外部用例实现真实 harness：stdout 最后一行输出 JSON verdict {status, reason, evidence}；status 仅允许 PASS / FAIL / SKIPPED / UNSUPPORTED。",
			"3. 把命令写入 external-config JSON（参照 tools/l4_external_config.example.json，但必须替换其中的 stub），用 L4_CONFIG=<path> 或 --config 传入。",
			"4. 先跑 make l4-preflight（清点）/ make l4-preflight-check（未齐备即非零退出），再补签 UD_L4_TOPOLOGY_ATTESTATION / UD_L4_REVIEW_SIGNATURE / UD_L4_PERF_ATTESTATION。",
			"5. 评审组按 ADR-0021 选择路径并签署，回写 docs/adr/README.md 索引状态；签署前不得改判据、不得把豁免计入通过数。",
			"6. 执行 make l4-run（--require-external --external-config --release-gate-only），真实证据落在 dist/l4_lab/；只有 14/14 PASS 且 NFR-PERF-01 达标才可能关闭序 6。",
		},
	}

	add := func(r LabRequirement) {
		rep.Requirements = append(rep.Requirements, r)
		switch r.Status {
		case LabPresent:
			rep.PresentCount++
		case LabMissing:
			rep.MissingCount++
		default:
			rep.UnverifiedCount++
		}
		if r.HumanAttestation {
			rep.ManualSignoff = append(rep.ManualSignoff, r.ID)
		}
	}

	// Tool-verifiable binary prerequisites.
	qb, ok := o.firstBinary("UD_L4_QBITTORRENT", "qbittorrent-nox", "qbittorrent")
	st, detail := LabMissing, "未找到 qBittorrent 可执行文件；可用 UD_L4_QBITTORRENT 指定绝对路径"
	if ok {
		st, detail = LabPresent, "找到 "+qb
	}
	add(LabRequirement{ID: "LAB-CLIENT-QB", Name: "真实 qBittorrent 客户端", Kind: "binary",
		Status: st, Detail: detail, RelatedCases: []string{"BT-01", "BT-02", "BT-13"}})

	tr, ok := o.firstBinary("UD_L4_TRANSMISSION", "transmission-daemon", "transmission-cli")
	st, detail = LabMissing, "未找到 Transmission 可执行文件；可用 UD_L4_TRANSMISSION 指定绝对路径"
	if ok {
		st, detail = LabPresent, "找到 "+tr
	}
	add(LabRequirement{ID: "LAB-CLIENT-TR", Name: "真实 Transmission / libtorrent 系客户端", Kind: "binary",
		Status: st, Detail: detail, RelatedCases: []string{"BT-03"}})

	tracker, ok := o.firstBinary("UD_L4_TRACKER", "chihaya", "opentracker")
	st, detail = LabMissing, "未找到 chihaya / opentracker 二进制；回环 fixture 不替代真实 tracker"
	if ok {
		st, detail = LabPresent, "找到 "+tracker
	}
	add(LabRequirement{ID: "LAB-TRACKER", Name: "自建真实 tracker 二进制", Kind: "binary",
		Status: st, Detail: detail, RelatedCases: []string{"BT-05", "BT-06", "BT-07"}})

	// Packet capture needs both a binary and the privilege to use it.
	capture, hasCapture := o.firstBinary("UD_L4_TCPDUMP", "tcpdump")
	switch {
	case !hasCapture:
		st, detail = LabMissing, "未找到 tcpdump / 抓包工具"
	case o.Euid() != 0:
		st, detail = LabUnverified, "找到 "+capture+"，但当前非特权用户；BPF 抓包权限须由实验室确认"
	default:
		st, detail = LabPresent, "找到 "+capture+" 且具备特权"
	}
	add(LabRequirement{ID: "LAB-CAPTURE", Name: "抓包能力（BT-08 / BT-14）", Kind: "capture",
		Status: st, Detail: detail, RelatedCases: []string{"BT-08", "BT-14"}})

	cfgStatus, cfgDetail := o.inspectExternalConfig(o.ExternalConfig)
	add(LabRequirement{ID: "LAB-CONFIG", Name: "外部 harness 配置（真实命令，非 stub）", Kind: "config",
		Status: cfgStatus, Detail: cfgDetail, RelatedCases: externalCaseIDs()})

	// Human-attestation prerequisites: a tool must never certify these.
	topoStatus, topoDetail := o.attestation("UD_L4_TOPOLOGY_ATTESTATION", "隔离千兆内网双机拓扑")
	add(LabRequirement{ID: "LAB-TOPOLOGY", Name: "隔离千兆内网 + 第二台主机", Kind: "topology",
		Status: topoStatus, Detail: topoDetail, RelatedCases: []string{"BT-01", "BT-10", "NFR-PERF-01"},
		HumanAttestation: true})

	reviewStatus, reviewDetail := o.attestation("UD_L4_REVIEW_SIGNATURE", "ADR-0021 评审签署结论")
	add(LabRequirement{ID: "LAB-REVIEW", Name: "判据 / 范围冲突的评审签署", Kind: "review",
		Status: reviewStatus, Detail: reviewDetail, RelatedCases: []string{"BT-02", "BT-03", "BT-11"},
		HumanAttestation: true})

	perfStatus, perfDetail := o.attestation("UD_L4_PERF_ATTESTATION", "NFR-PERF-01 双机 1GiB ≥ 80MB/s 实测")
	add(LabRequirement{ID: "LAB-PERF", Name: "NFR-PERF-01 双机千兆基准", Kind: "benchmark",
		Status: perfStatus, Detail: perfDetail, RelatedCases: []string{"NFR-PERF-01"},
		HumanAttestation: true})

	add(LabRequirement{ID: "LAB-CLIENT-LT", Name: "libtorrent 2.x 双向会话客户端", Kind: "client-session",
		Status:       LabUnverified,
		Detail:       "BX-03 / BX-04 的 python-libtorrent wheel 只作单向 seeder fixture；L4 需要可独立运行并完成双向会话的真实客户端，且我方做种半向按 ADR-0004 / ADR-0017 冲突仍未裁决",
		RelatedCases: []string{"BT-03"}, HumanAttestation: true})

	ready := true
	for _, r := range rep.Requirements {
		if !r.HumanAttestation && r.Status != LabPresent {
			ready = false
		}
	}
	rep.Ready = ready
	return rep
}

// Markdown renders a human-readable inventory. The invariants are printed
// verbatim so the document cannot be quoted as "L4 passed".
func (r PreflightReport) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# WBS 11 · L4 实验室前置条件清点\n\n")
	fmt.Fprintf(&b, "生成时间：%s ｜ schema：`%s`\n\n", r.GeneratedAt, r.Schema)
	fmt.Fprintf(&b, "> **这不是 L4 执行证据**：`l4_executed = %v`、`release_gate_met = %v` 为硬不变量。\n", r.L4Executed, r.ReleaseGateMet)
	fmt.Fprintf(&b, "> `PRESENT` 只表示工具可核验的条件存在，**绝不等于 PASS**；\n")
	fmt.Fprintf(&b, "> `SKIPPED` / `UNSUPPORTED` / `UNVERIFIED` 一律不计为通过。\n")
	fmt.Fprintf(&b, "> 发布结论只以 `dist/interop.json:release_gate_met` 为准。\n\n")
	fmt.Fprintf(&b, "## 汇总\n\n| 项 | 值 |\n|---|---|\n")
	fmt.Fprintf(&b, "| 工具可核验条件齐备（ready） | %v |\n", r.Ready)
	fmt.Fprintf(&b, "| PRESENT / MISSING / UNVERIFIED | %d / %d / %d |\n",
		r.PresentCount, r.MissingCount, r.UnverifiedCount)
	fmt.Fprintf(&b, "| L4 已执行 | **%v** |\n| 发布门禁 | **%v** |\n\n", r.L4Executed, r.ReleaseGateMet)
	fmt.Fprintf(&b, "**判据阻塞**：%s\n\n", r.ReviewBlocker)
	fmt.Fprintf(&b, "## 逐项清点\n\n| 编号 | 条件 | 类型 | 状态 | 人工签署 | 说明 |\n|---|---|---|---|---|---|\n")
	for _, x := range r.Requirements {
		human := "—"
		if x.HumanAttestation {
			human = "是"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
			x.ID, x.Name, x.Kind, x.Status, human, escapeCell(x.Detail))
	}
	fmt.Fprintf(&b, "\n## 实验室执行步骤\n\n")
	for _, step := range r.Runbook {
		fmt.Fprintf(&b, "- %s\n", step)
	}
	return b.String()
}

// WritePreflightEvidence writes l4_preflight.json and L4_PREFLIGHT.md.
func (r PreflightReport) WritePreflightEvidence(jsonPath, mdPath string) error {
	for _, p := range []string{jsonPath, mdPath} {
		if dir := filepath.Dir(p); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create evidence dir: %w", err)
			}
		}
	}
	blob, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal preflight: %w", err)
	}
	if err := os.WriteFile(jsonPath, append(blob, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", jsonPath, err)
	}
	if err := os.WriteFile(mdPath, []byte(r.Markdown()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", mdPath, err)
	}
	return nil
}
