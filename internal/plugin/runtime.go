package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/asdfgh129/ud/internal/core"
)

// Doer is the single egress seam. Production uses an SSRF-Guard-backed
// http.Client; `udctl plugin test` injects a recorded-replay Doer so tests
// never touch a real site (docs/08 §8).
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Engine holds the trusted host services handed to every plugin invocation.
// None of these are reachable from JavaScript except through the injected ctx.
type Engine struct {
	Guard  core.URLGuard
	Doer   Doer
	Logger *slog.Logger
}

// Runtime is a compiled, immutable rule pack. It implements core.Resolver and
// is safe for concurrent Resolve calls: each call creates a fresh goja VM.
type Runtime struct {
	manifest *Manifest
	program  *goja.Program
	matchers []*regexp.Regexp
	engine   Engine
}

// Compile validates, transforms and compiles one rule pack. No code executes
// at compile time.
func Compile(m *Manifest, script []byte, e Engine) (*Runtime, error) {
	if m == nil {
		return nil, core.Err(core.E_INVALID_INPUT, "nil manifest")
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	wrapped, err := Transform(string(script))
	if err != nil {
		return nil, err
	}
	program, err := goja.Compile(m.Entry, wrapped, false)
	if err != nil {
		return nil, core.Err(core.E_INVALID_INPUT, "compile %s: %v", m.Entry, err)
	}
	return &Runtime{
		manifest: m, program: program, matchers: m.MatchRule(), engine: e,
	}, nil
}

// Manifest returns a copy of the validated manifest for diagnostics.
func (r *Runtime) Manifest() Manifest { return *r.manifest }

func (r *Runtime) Name() string { return "plugin:" + r.manifest.Name }

// Match applies the manifest regexes to the raw input. Built-in Resolvers
// always run first (see registry.go); match rules overlapping their schemes
// are already rejected by Manifest.Validate.
func (r *Runtime) Match(in core.Input) bool {
	for _, re := range r.matchers {
		if re.MatchString(in.Raw) {
			return true
		}
	}
	return false
}

var (
	errPluginTimeout   = errors.New("plugin execution timeout")
	errPluginCancelled = errors.New("plugin execution cancelled")
)

// Resolve runs the rule once in a fresh VM with an interrupt-driven deadline,
// a host-visible memory budget, and full output validation.
func (r *Runtime) Resolve(rc core.ResolveContext, in core.Input) (res *core.Resource, err error) {
	if !r.Match(in) {
		return nil, core.Err(core.E_RESOLVE_FAILED, "plugin %s does not match this input", r.manifest.Name)
	}
	parent := rc.Ctx
	if parent == nil {
		parent = context.Background()
	}
	timeout := time.Duration(r.manifest.Limits.TimeoutMs) * time.Millisecond
	runCtx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	vm := goja.New()
	// goja has no per-VM heap cap; memoryMB bounds every host-visible
	// allocation (responses, output) and the call-stack depth. True hard
	// isolation requires the WASM upgrade path (ADR-0014 §5).
	stack := r.manifest.Limits.MemoryMB * 8
	if stack < 64 {
		stack = 64
	}
	if stack > 1024 {
		stack = 1024
	}
	vm.SetMaxCallStackSize(stack)

	host := &hostState{
		runtime: r, vm: vm, ctx: runCtx,
		budget:  int64(r.manifest.Limits.MemoryMB) << 20,
		maxBody: r.manifest.Limits.MaxResponseBytes,
	}
	ctxVal := r.buildContext(vm, host)

	// A goja VM is interrupted from a watchdog goroutine; Interrupt is
	// explicitly documented as safe to call while JavaScript is running.
	done := make(chan struct{})
	var once sync.Once
	interrupt := func(v any) {
		once.Do(func() { vm.Interrupt(v) })
	}
	timer := time.AfterFunc(timeout, func() { interrupt(errPluginTimeout) })
	go func() {
		select {
		case <-runCtx.Done():
			interrupt(errPluginCancelled)
		case <-done:
		}
	}()
	defer func() {
		timer.Stop()
		close(done)
		vm.ClearInterrupt()
	}()

	// Catch ANY Go-side panic (goja internals included) and turn it into a
	// classified error, per docs/08 §3 host obligation ③.
	defer func() {
		if rec := recover(); rec != nil {
			res = nil
			err = core.Err(core.E_RESOLVE_FAILED,
				"plugin %s panicked: %v", r.manifest.Name, rec)
		}
	}()

	if _, runErr := vm.RunProgram(r.program); runErr != nil {
		return nil, r.classify(runErr, host)
	}
	holder := vm.Get("__ud_plugin")
	if holder == nil || goja.IsUndefined(holder) || goja.IsNull(holder) {
		return nil, core.Err(core.E_RESOLVE_FAILED, "plugin %s produced no module", r.manifest.Name)
	}
	obj := holder.ToObject(vm)
	fn, ok := goja.AssertFunction(obj.Get("resolve"))
	if !ok {
		return nil, core.Err(core.E_INVALID_INPUT,
			"plugin %s must export a resolve(url, ctx) function", r.manifest.Name)
	}
	value, callErr := fn(goja.Undefined(), vm.ToValue(in.Raw), ctxVal)
	if callErr != nil {
		return nil, r.classify(callErr, host)
	}
	if host.err != nil {
		return nil, host.err
	}
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, core.Err(core.E_RESOLVE_FAILED, "plugin %s returned no Resource", r.manifest.Name)
	}
	return r.toResource(rc, value, host)
}

// buildContext injects ONLY the capabilities granted by the manifest.
// fs / env / subprocess / require / process / globalThis do not exist: goja
// has no such globals and the static scan rejects attempts to reach them.
func (r *Runtime) buildContext(vm *goja.Runtime, host *hostState) goja.Value {
	ctxObj := vm.NewObject()
	m := r.manifest

	if m.Capabilities.HTTP != nil {
		httpObj := vm.NewObject()
		_ = httpObj.Set("get", func(call goja.FunctionCall) goja.Value {
			return host.httpGet(call)
		})
		_ = ctxObj.Set("http", httpObj)
	}

	logObj := vm.NewObject()
	for _, level := range []string{"info", "warn", "error"} {
		lvl := level
		_ = logObj.Set(lvl, func(call goja.FunctionCall) goja.Value {
			if r.engine.Logger != nil && len(call.Arguments) > 0 {
				msg := truncate(call.Arguments[0].String(), 1024)
				r.engine.Logger.Log(context.Background(), logLevel(lvl),
					"plugin log", "plugin", m.Name, "version", m.Version, "message", msg)
			}
			return goja.Undefined()
		})
	}
	_ = ctxObj.Set("log", logObj)

	b64 := vm.NewObject()
	_ = b64.Set("encode", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		s := call.Arguments[0].String()
		if int64(len(s)) > host.maxBody || !host.spend(int64(len(s))) {
			host.fail(core.Err(core.E_INVALID_INPUT, "base64 input exceeds plugin memory budget"))
			return goja.Undefined()
		}
		return vm.ToValue(base64.StdEncoding.EncodeToString([]byte(s)))
	})
	_ = b64.Set("decode", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		b, e := base64.StdEncoding.DecodeString(call.Arguments[0].String())
		if e != nil || !host.spend(int64(len(b))) {
			host.fail(core.Err(core.E_INVALID_INPUT, "invalid base64 or memory budget exceeded"))
			return goja.Undefined()
		}
		return vm.ToValue(string(b))
	})
	if grantsCrypto(m, "base64") {
		_ = ctxObj.Set("base64", b64)
	}
	if grantsCrypto(m, "sha256") {
		_ = ctxObj.Set("sha256", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				return goja.Undefined()
			}
			s := call.Arguments[0].String()
			if int64(len(s)) > host.maxBody || !host.spend(int64(len(s))) {
				host.fail(core.Err(core.E_INVALID_INPUT, "sha256 input exceeds plugin memory budget"))
				return goja.Undefined()
			}
			sum := sha256.Sum256([]byte(s))
			return vm.ToValue(hex.EncodeToString(sum[:]))
		})
	}
	return ctxObj
}

func grantsCrypto(m *Manifest, name string) bool {
	for _, c := range m.Capabilities.Crypto {
		if c == name {
			return true
		}
	}
	return false
}

func logLevel(s string) slog.Level {
	switch s {
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// classify turns a goja error into a stable wire error. Host denials always
// win so a plugin cannot swallow a security rejection by catching it.
func (r *Runtime) classify(runErr error, host *hostState) error {
	if host.err != nil {
		return host.err
	}
	var interrupted *goja.InterruptedError
	if errors.As(runErr, &interrupted) {
		v := interrupted.Value()
		if errors.Is(asError(v), errPluginTimeout) || errors.Is(runErr, context.DeadlineExceeded) {
			return core.Err(core.E_RESOLVE_FAILED,
				"plugin %s exceeded timeoutMs=%d", r.manifest.Name, r.manifest.Limits.TimeoutMs)
		}
		return core.Err(core.E_RESOLVE_FAILED, "plugin %s was cancelled", r.manifest.Name)
	}
	msg := runErr.Error()
	var exc *goja.Exception
	if errors.As(runErr, &exc) && exc.Value() != nil {
		msg = exc.Value().String()
	}
	msg = truncate(msg, 512)
	if strings.Contains(msg, "RULE_OUTDATED") {
		return ruleOutdated(r.manifest, msg)
	}
	return core.Err(core.E_RESOLVE_FAILED, "plugin %s failed: %s", r.manifest.Name, msg)
}

func asError(v any) error {
	if e, ok := v.(error); ok {
		return e
	}
	return nil
}

// RuleOutdatedDetail is attached to every rule-obsolescence error so callers
// can trigger a fallback Resolver instead of silently dropping the task.
const RuleOutdatedDetail = "RULE_OUTDATED"

func ruleOutdated(m *Manifest, msg string) error {
	return core.Err(core.E_CAPABILITY_UNAVAILABLE,
		"plugin %s v%s rule is outdated: %s", m.Name, m.Version, msg).
		WithDetail("reason", RuleOutdatedDetail).
		WithDetail("plugin", m.Name).
		WithDetail("version", m.Version)
}

// IsRuleOutdated reports whether an error is a rule-obsolescence signal.
func IsRuleOutdated(err error) bool {
	e := core.AsError(err)
	if e == nil {
		return false
	}
	return e.Details["reason"] == RuleOutdatedDetail
}

// toResource marshals the JS result through JSON (bounded by the memory
// budget) and validates every field before building the frozen Resource.
func (r *Runtime) toResource(rc core.ResolveContext, value goja.Value, host *hostState) (*core.Resource, error) {
	raw, err := json.Marshal(value.Export())
	if err != nil {
		return nil, core.Err(core.E_RESOLVE_FAILED,
			"plugin %s returned a non-serialisable Resource: %v", r.manifest.Name, err)
	}
	if !host.spend(int64(len(raw))) || int64(len(raw)) > host.maxBody {
		return nil, core.Err(core.E_INVALID_INPUT,
			"plugin %s output exceeds maxResponseBytes/memory budget", r.manifest.Name)
	}
	var out pluginResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, core.Err(core.E_RESOLVE_FAILED,
			"plugin %s returned an invalid Resource: %v", r.manifest.Name, err)
	}
	return buildResource(rc, r.manifest, &out, r.engine.Guard)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
