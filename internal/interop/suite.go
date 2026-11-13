// Package interop implements the WBS 11 interoperability suite v1.
//
// The suite separates two things that must never be conflated:
//
//   - Gate cases (BT-01..BT-14, docs/12 §3.1) are the L4 release criteria. Most
//     of them require real third-party clients, a self-hosted tracker binary,
//     packet capture and a gigabit LAN. When that environment is absent they are
//     reported as SKIPPED, never as PASS.
//   - Local cases (L3-xx) are fully automated loopback evidence: self-hosted
//     BEP 3 / BEP 15 trackers plus a real BitTorrent wire transfer against an
//     independent seeder. They are valuable regression evidence but do NOT
//     satisfy the L4 gate.
//
// ReleaseGateMet is true only when every gate case is PASS. A green `make
// interop` therefore means "the automatable suite is healthy", not "P1 is
// released"; use --require-external to enforce the real release gate.
package interop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Scope describes where a case can be executed.
type Scope string

const (
	ScopeInProcess  Scope = "in-process"
	ScopeExternal   Scope = "external"
	ScopeProductGap Scope = "product-gap"
)

// Status is deliberately explicit: there is no ambiguous "OK".
type Status string

const (
	StatusPass        Status = "PASS"
	StatusFail        Status = "FAIL"
	StatusSkipped     Status = "SKIPPED"
	StatusUnsupported Status = "UNSUPPORTED"
)

// Evidence carries machine-readable measurements for a case.
type Evidence map[string]any

// Outcome is what a case implementation returns.
type Outcome struct {
	Status   Status
	Reason   string
	Evidence Evidence
}

func pass(reason string, ev Evidence) Outcome {
	return Outcome{Status: StatusPass, Reason: reason, Evidence: ev}
}

func fail(reason string) Outcome {
	return Outcome{Status: StatusFail, Reason: reason}
}

// Options controls a suite run.
type Options struct {
	// RequireExternal enables the external-client harness. Without it every
	// external case is SKIPPED with an explicit reason.
	RequireExternal bool
	// ExternalConfig is a JSON file mapping a case ID to a command that runs
	// the real third-party client and prints a verdict (see external.go).
	ExternalConfig string
	// CaseTimeout bounds one case. Zero means DefaultCaseTimeout.
	CaseTimeout time.Duration
	// OutDir receives interop.json and INTEROP.md. Empty disables writing.
	OutDir string
}

// DefaultCaseTimeout bounds a single loopback case.
const DefaultCaseTimeout = 90 * time.Second

// Case is one row of the interoperability matrix.
type Case struct {
	ID        string
	Title     string
	Criterion string
	Scope     Scope
	// Gate marks a canonical L4 release criterion from docs/12 §3.1.
	Gate     bool
	Requires string
	Run      func(ctx context.Context, o Options) Outcome
}

// Result is the serialised outcome of one case.
type Result struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Criterion  string   `json:"criterion"`
	Scope      Scope    `json:"scope"`
	Gate       bool     `json:"gate"`
	Status     Status   `json:"status"`
	Reason     string   `json:"reason"`
	Evidence   Evidence `json:"evidence,omitempty"`
	DurationMs int64    `json:"duration_ms"`
}

// Summary counts outcomes. Skipped and unsupported are never counted as pass.
type Summary struct {
	Total       int `json:"total"`
	GateTotal   int `json:"gate_total"`
	GatePass    int `json:"gate_pass"`
	Pass        int `json:"pass"`
	Fail        int `json:"fail"`
	Skipped     int `json:"skipped"`
	Unsupported int `json:"unsupported"`
}

// Report is the machine-readable evidence document.
type Report struct {
	Schema            string   `json:"schema"`
	GeneratedAt       string   `json:"generated_at"`
	RequireExternal   bool     `json:"require_external"`
	Results           []Result `json:"results"`
	Summary           Summary  `json:"summary"`
	ReleaseGateMet    bool     `json:"release_gate_met"`
	ReleaseGateReason string   `json:"release_gate_reason"`
	// ExternalEvidence holds genuine cross-client verdicts produced by cmd/udinteropx
	// against a real third-party BitTorrent engine (e.g. aria2-next bundled with
	// MotrixNext). These are SUPPLEMENTARY: they never satisfy the named-client L4
	// gate cases BT-01..BT-14 and do not flip ReleaseGateMet.
	ExternalEvidence []ExternalVerdict `json:"external_evidence,omitempty"`
}

// ExternalVerdict is one honest result from the real cross-client harness.
// SKIPPED / UNSUPPORTED are never coerced to PASS; only a hash-verified
// download is PASS.
type ExternalVerdict struct {
	ID       string `json:"id"`
	Client   string `json:"client"`
	Status   string `json:"status"` // PASS | FAIL | SKIPPED | UNSUPPORTED
	Name     string `json:"name"`
	Evidence string `json:"evidence"`
	InfoHash string `json:"infohash,omitempty"`
	Error    string `json:"error,omitempty"`
}

const schemaVersion = "ud.interop/v1"

// Registry returns the ordered case list. Keep BT-01..BT-14 in docs/12 §3.1
// order; L3 cases follow as supplementary local evidence.
func Registry() []Case {
	cases := []Case{
		gateExternal("BT-01", "与 qBittorrent 双向：我方下载 / 对方做种", "文件哈希一致"),
		productGap("BT-02", "与 qBittorrent 双向：对方下载 / 我方做种",
			"对方报告完成、bytes_uploaded 一致",
			"我方做种/上传未实现：clientConfig 固定 NoUpload=true、Seed=false（ADR-0004、WBS 7 交付边界）"),
		gateExternal("BT-03", "与 Transmission / libtorrent 系客户端双向", "同上"),
		gateExternal("BT-04", "磁力：仅 DHT 获取元数据并完成下载", "无 .torrent 文件也可用"),
		gateExternal("BT-05", "自建 chihaya / opentracker：HTTP announce + scrape", "统计准确"),
		gateExternal("BT-06", "UDP tracker（IPv4 / IPv6 compact peer list）", "peer 列表解析正确"),
		gateExternal("BT-07", "HTTPS tracker + passkey + 多 tracker 分层降级", "主 tracker 挂掉后切备"),
		gateExternal("BT-08", "private=1：DHT / PEX / LSD 全部关闭", "抓包验证无 DHT/PEX 流量"),
		gateExternal("BT-09", "选择下载（多文件种子部分选中）", "未选文件不落盘"),
		gateExternal("BT-10", "WebSeed / uTP / MSE 连接", "至少一条链路成功"),
		productGap("BT-11", "做种策略：分享率与 seed-time 达标后停止", "统计取统一 Task 模型",
			"做种默认关闭且不提供分享率 / seed-time / 上传槽策略（WBS 7 交付边界）"),
		gateExternal("BT-12", "流式读取边下边播（File.NewReader 语义）", "首字节可读时间 < 阈值"),
		gateExternal("BT-13", "BT v2 hybrid 与 qBittorrent 互操作", "已知支持不整齐，记录结果并默认 v1/hybrid"),
		gateExternal("BT-14", "诚实 PeerID 校验", "抓包为 -GT0001- 或自定义但非伪装"),
	}
	cases = append(cases, LocalCases()...)
	return cases
}

func gateExternal(id, title, criterion string) Case {
	return Case{
		ID: id, Title: title, Criterion: criterion,
		Scope: ScopeExternal, Gate: true,
		Requires: "隔离网络 + 真实第三方客户端 / tracker 二进制 + 抓包；" +
			"设 UD_INTEROP_EXTERNAL=1 并提供 --external-config",
		Run: runExternalCase,
	}
}

func productGap(id, title, criterion, reason string) Case {
	return Case{
		ID: id, Title: title, Criterion: criterion,
		Scope: ScopeProductGap, Gate: true, Requires: reason,
		Run: func(context.Context, Options) Outcome {
			return Outcome{Status: StatusUnsupported, Reason: reason}
		},
	}
}

// RunAll executes every case, isolating panics and per-case timeouts. It never
// returns an error for a failing case; failures are data in the report.
func RunAll(ctx context.Context, o Options) (Report, error) {
	timeout := o.CaseTimeout
	if timeout <= 0 {
		timeout = DefaultCaseTimeout
	}
	results := make([]Result, 0, len(Registry()))
	for _, c := range Registry() {
		start := time.Now()
		r := Result{ID: c.ID, Title: c.Title, Criterion: c.Criterion,
			Scope: c.Scope, Gate: c.Gate}
		if c.Run == nil {
			r.Status, r.Reason = StatusSkipped, c.Requires
		} else {
			caseCtx, cancel := context.WithTimeout(withCaseID(ctx, c.ID), timeout)
			out := runSafely(caseCtx, c, o)
			cancel()
			r.Status, r.Reason, r.Evidence = out.Status, out.Reason, out.Evidence
		}
		if r.Reason == "" {
			r.Reason = "(no reason supplied)"
		}
		r.DurationMs = time.Since(start).Milliseconds()
		results = append(results, r)
	}

	rep := Report{
		Schema:          schemaVersion,
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		RequireExternal: o.RequireExternal,
		Results:         results,
	}
	for _, r := range results {
		rep.Summary.Total++
		if r.Gate {
			rep.Summary.GateTotal++
			if r.Status == StatusPass {
				rep.Summary.GatePass++
			}
		}
		switch r.Status {
		case StatusPass:
			rep.Summary.Pass++
		case StatusFail:
			rep.Summary.Fail++
		case StatusSkipped:
			rep.Summary.Skipped++
		case StatusUnsupported:
			rep.Summary.Unsupported++
		}
	}

	var unmet []string
	for _, r := range results {
		if r.Gate && r.Status != StatusPass {
			unmet = append(unmet, fmt.Sprintf("%s=%s", r.ID, r.Status))
		}
	}
	rep.ReleaseGateMet = rep.Summary.Fail == 0 && len(unmet) == 0
	switch {
	case rep.Summary.Fail > 0:
		rep.ReleaseGateReason = fmt.Sprintf("%d 个用例 FAIL；发布门禁不通过", rep.Summary.Fail)
	case len(unmet) > 0:
		rep.ReleaseGateReason = "L4 发布门禁未通过（SKIPPED / UNSUPPORTED 不计为 PASS）：" + strings.Join(unmet, ", ")
	default:
		rep.ReleaseGateReason = "BT-01..BT-14 全部 PASS"
	}

	// Best-effort: fold in genuine cross-client evidence from cmd/udinteropx
	// (dist/interop_external.json) when present. This never affects the L4 gate.
	if o.OutDir != "" {
		if ext, err := loadExternalEvidence(filepath.Join(o.OutDir, "interop_external.json")); err == nil {
			rep.ExternalEvidence = ext
		}
	}

	if o.OutDir != "" {
		if err := rep.WriteEvidence(o.OutDir); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func runSafely(ctx context.Context, c Case, o Options) (out Outcome) {
	defer func() {
		if v := recover(); v != nil {
			out = fail(fmt.Sprintf("case panicked: %v", v))
		}
	}()
	out = c.Run(ctx, o)
	if out.Status == "" {
		return fail("case returned an empty status")
	}
	if err := ctx.Err(); err != nil && out.Status == StatusPass {
		return fail("case exceeded its timeout: " + err.Error())
	}
	return out
}

// WriteEvidence writes interop.json and INTEROP.md into dir.
func (r Report) WriteEvidence(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create evidence dir: %w", err)
	}
	blob, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal evidence: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "interop.json"), append(blob, '\n'), 0o644); err != nil {
		return fmt.Errorf("write interop.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "INTEROP.md"), []byte(r.Markdown()), 0o644); err != nil {
		return fmt.Errorf("write INTEROP.md: %w", err)
	}
	return nil
}

// Markdown renders a human-readable evidence document.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# WBS 11 互操作套件 v1 · 执行证据\n\n")
	fmt.Fprintf(&b, "生成时间：%s ｜ schema：`%s` ｜ 外部 harness：%v\n\n", r.GeneratedAt, r.Schema, r.RequireExternal)
	fmt.Fprintf(&b, "> **口径**：`BT-01..BT-14` 是 `12` §3.1 的 **L4 发布门禁**判据；\n")
	fmt.Fprintf(&b, "> `L3-xx` 是纯回环、可复跑的**补充本地证据**，不等价于与真实公网客户端互通。\n")
	fmt.Fprintf(&b, "> `SKIPPED` / `UNSUPPORTED` **一律不计为通过**，不得据此宣称「已全矩阵通过」。\n")
	fmt.Fprintf(&b, "> NFR-PERF-01 的千兆内网 1GiB ≥ 80MB/s 基准需要隔离千兆网络，本套件不替代。\n\n")
	fmt.Fprintf(&b, "## 汇总\n\n")
	fmt.Fprintf(&b, "| 项 | 值 |\n|---|---|\n")
	fmt.Fprintf(&b, "| 用例总数 | %d |\n", r.Summary.Total)
	fmt.Fprintf(&b, "| L4 门禁通过 | %d / %d |\n", r.Summary.GatePass, r.Summary.GateTotal)
	fmt.Fprintf(&b, "| PASS | %d |\n| FAIL | %d |\n| SKIPPED | %d |\n| UNSUPPORTED | %d |\n",
		r.Summary.Pass, r.Summary.Fail, r.Summary.Skipped, r.Summary.Unsupported)
	fmt.Fprintf(&b, "| **发布门禁** | **%s** |\n", releaseVerdict(r.ReleaseGateMet))
	fmt.Fprintf(&b, "| 结论 | %s |\n\n", r.ReleaseGateReason)

	fmt.Fprintf(&b, "## 逐条结果\n\n")
	fmt.Fprintf(&b, "| 编号 | 门禁 | Scope | 状态 | 用时(ms) | 说明 |\n|---|---|---|---|---|---|\n")
	for _, x := range r.Results {
		gate := "—"
		if x.Gate {
			gate = "L4"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %s |\n",
			x.ID, gate, x.Scope, x.Status, x.DurationMs, escapeCell(x.Reason))
	}

	if len(r.ExternalEvidence) > 0 {
		fmt.Fprintf(&b, "\n## 真实跨客户端证据（补充，非 L4 门禁）\n\n")
		fmt.Fprintf(&b, "> 以下由 `cmd/udinteropx` 驱动**真实的第三方 BitTorrent 引擎**作为做种方、\n")
		fmt.Fprintf(&b, "> 以本项目的真实 `bttransport` 作为下载方，在回环自托管 BEP 3 tracker 下完成下载并校验哈希。\n")
		fmt.Fprintf(&b, "> aria2-next 不点名于 L4；libtorrent 2.x 虽点名于 `BT-03`，但本段只覆盖**单向**\n")
		fmt.Fprintf(&b, "> （对方做种 → 我方下载），而 `BT-03` 要求双向（我方做种 → 对方下载，按 ADR-0004 明确不做），\n")
		fmt.Fprintf(&b, "> 且仍缺隔离千兆内网、抓包设备与 `chihaya` / `opentracker` 二进制。\n")
		fmt.Fprintf(&b, "> 因此这是**真实跨客户端互通证据**，但**不计入** `BT-01..BT-14` 的 L4 发布门禁；\n")
		fmt.Fprintf(&b, "> L4 仍以 SKIPPED / UNSUPPORTED 维持**未通过**，`UNSUPPORTED` 表示外部引擎不可用，绝不等于通过。\n\n")
		fmt.Fprintf(&b, "| 编号 | 客户端 | 状态 | 说明 | 哈希/infohash |\n|---|---|---|---|---|\n")
		for _, v := range r.ExternalEvidence {
			note := v.Name
			if v.Status != "PASS" {
				detail := v.Error
				if detail == "" {
					detail = v.Evidence
				}
				if runes := []rune(detail); len(runes) > 160 {
					detail = string(runes[:160]) + "…"
				}
				if detail != "" {
					note += " — " + detail
				}
			}
			ref := v.InfoHash
			if ref == "" {
				ref = v.Evidence
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | `%s` |\n",
				v.ID, v.Client, v.Status, escapeCell(note), ref)
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "\n## 证据明细\n\n")
	for _, x := range r.Results {
		if len(x.Evidence) == 0 {
			continue
		}
		fmt.Fprintf(&b, "### %s · %s\n\n```json\n", x.ID, x.Title)
		keys := make([]string, 0, len(x.Evidence))
		for k := range x.Evidence {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ordered := make(map[string]any, len(keys))
		for _, k := range keys {
			ordered[k] = x.Evidence[k]
		}
		blob, _ := json.MarshalIndent(ordered, "", "  ")
		b.Write(blob)
		fmt.Fprintf(&b, "\n```\n\n")
	}
	return b.String()
}

func releaseVerdict(met bool) string {
	if met {
		return "✅ 通过"
	}
	return "❌ 未通过（预期：本地环境不具备外部 L4 条件）"
}

// loadExternalEvidence reads dist/interop_external.json (produced by cmd/udinteropx
// against a real third-party client). Best-effort: errors return nil so a missing
// or malformed file never breaks the main loopback run.
func loadExternalEvidence(path string) ([]ExternalVerdict, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var vs []ExternalVerdict
	if err := json.Unmarshal(blob, &vs); err != nil {
		return nil, err
	}
	for _, v := range vs {
		switch v.Status {
		case "PASS", "FAIL", "SKIPPED", "UNSUPPORTED":
		default:
			return nil, fmt.Errorf("未知 status %q in %s", v.Status, path)
		}
	}
	return vs, nil
}

func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// LoadReport reads a previously written interop.json (used by tooling/tests).
func LoadReport(path string) (Report, error) {
	var r Report
	blob, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(blob, &r); err != nil {
		return r, fmt.Errorf("parse %s: %w", path, err)
	}
	return r, nil
}

// ---------------------------------------------------------------------------
// External harness
// ---------------------------------------------------------------------------

type externalConfig struct {
	Cases map[string]externalCase `json:"cases"`
}

type externalCase struct {
	Command  []string `json:"command"`
	TimeoutS int      `json:"timeout_s,omitempty"`
	Note     string   `json:"note,omitempty"`
}

type externalVerdict struct {
	Status   Status   `json:"status"`
	Reason   string   `json:"reason"`
	Evidence Evidence `json:"evidence,omitempty"`
}

func runExternalCase(ctx context.Context, o Options) Outcome {
	if !o.RequireExternal {
		return Outcome{Status: StatusSkipped,
			Reason: "未启用外部 harness（UD_INTEROP_EXTERNAL / --require-external）；真实客户端矩阵未执行"}
	}
	if o.ExternalConfig == "" {
		return fail("已启用外部 harness 但未提供 --external-config")
	}
	cfg, err := loadExternalConfig(o.ExternalConfig)
	if err != nil {
		return fail("外部 harness 配置不可用：" + err.Error())
	}
	id := caseIDFromContext(ctx)
	spec, ok := cfg.Cases[id]
	if !ok || len(spec.Command) == 0 {
		return Outcome{Status: StatusSkipped, Reason: "外部 harness 配置未包含该用例：" + id}
	}
	limit := time.Duration(spec.TimeoutS) * time.Second
	if limit <= 0 {
		limit = 5 * time.Minute
	}
	caseCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	cmd := exec.CommandContext(caseCtx, spec.Command[0], spec.Command[1:]...)
	cmd.Env = append(os.Environ(), "UD_INTEROP_CASE_ID="+id)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	verdict, parseErr := parseVerdict(stdout.Bytes())
	if parseErr != nil {
		return fail(fmt.Sprintf("外部用例 %s 未输出合法 verdict：%v；stderr=%s",
			id, parseErr, tailString(stderr.String(), 300)))
	}
	if runErr != nil && verdict.Status == StatusPass {
		verdict.Status = StatusFail
		verdict.Reason = fmt.Sprintf("外部命令退出失败：%v；%s", runErr, verdict.Reason)
	}
	if verdict.Reason == "" {
		verdict.Reason = "外部 harness 返回 " + string(verdict.Status)
	}
	if spec.Note != "" {
		verdict.Reason += "（" + spec.Note + "）"
	}
	return Outcome{Status: verdict.Status, Reason: verdict.Reason, Evidence: verdict.Evidence}
}

func loadExternalConfig(path string) (externalConfig, error) {
	var cfg externalConfig
	blob, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(blob, &cfg); err != nil {
		return cfg, err
	}
	if len(cfg.Cases) == 0 {
		return cfg, errors.New("cases 为空")
	}
	return cfg, nil
}

// parseVerdict reads the last JSON object line from a harness' stdout.
func parseVerdict(stdout []byte) (externalVerdict, error) {
	var v externalVerdict
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var last string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "{") {
			last = line
		}
	}
	if last == "" {
		return v, errors.New("stdout 中没有 JSON verdict 行")
	}
	if err := json.Unmarshal([]byte(last), &v); err != nil {
		return v, err
	}
	switch v.Status {
	case StatusPass, StatusFail, StatusSkipped, StatusUnsupported:
	default:
		return v, fmt.Errorf("未知 status %q", v.Status)
	}
	return v, nil
}

func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

type caseIDKey struct{}

func withCaseID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, caseIDKey{}, id)
}

func caseIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(caseIDKey{}).(string); ok {
		return v
	}
	return ""
}
