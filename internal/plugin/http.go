package plugin

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/asdfgh129/ud/internal/core"
)

// httpClientProvider is an optional extension of core.URLGuard implemented by
// security.SSRFGuard. Keeping it optional means a test/fixture guard only has
// to satisfy the frozen core.URLGuard interface.
type httpClientProvider interface {
	NewHTTPClient(timeout time.Duration) *http.Client
}

// hostState carries per-invocation, host-visible state. `err` is the single
// channel through which a denied capability is surfaced: host functions never
// throw into JavaScript, they record the first denial and return undefined.
// Resolve checks it before looking at any plugin output, so a rule cannot
// swallow a security rejection.
type hostState struct {
	runtime *Runtime
	vm      *goja.Runtime
	ctx     context.Context

	mu      sync.Mutex
	used    int64
	budget  int64
	maxBody int64
	err     *core.Error
}

func (h *hostState) spend(n int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if n < 0 || h.used+n > h.budget {
		return false
	}
	h.used += n
	return true
}

func (h *hostState) fail(e *core.Error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.err == nil {
		h.err = e
	}
}

var sensitiveHeaders = map[string]bool{
	"authorization": true, "proxy-authorization": true, "cookie": true,
	"set-cookie": true, "host": true, "x-api-key": true,
}

// httpGet implements ctx.http.get(url, {headers}) -> {status, body, headers}.
// Every hop (initial URL and each redirect) is checked against BOTH the
// manifest allowedHosts whitelist and the shared SSRF Guard, which pins DNS
// before dialing. This is the only network capability a plugin can ever get.
func (h *hostState) httpGet(call goja.FunctionCall) goja.Value {
	m := h.runtime.manifest
	if m.Capabilities.HTTP == nil {
		h.fail(core.Err(core.E_RESOLVE_SSRF_BLOCKED, "plugin has no http capability"))
		return goja.Undefined()
	}
	if len(call.Arguments) == 0 || goja.IsUndefined(call.Arguments[0]) {
		h.fail(core.Err(core.E_INVALID_INPUT, "ctx.http.get requires a URL"))
		return goja.Undefined()
	}
	raw := call.Arguments[0].String()
	if len(raw) > 8192 {
		h.fail(core.Err(core.E_RESOLVE_SSRF_BLOCKED, "request URL exceeds 8192 bytes"))
		return goja.Undefined()
	}

	headers := parseHeaders(h, call)
	if h.err != nil {
		return goja.Undefined()
	}

	maxRedirects := m.Capabilities.HTTP.MaxRedirects
	if g := h.runtime.engine.Guard; g != nil {
		if n := g.MaxRedirects(); n >= 0 && n < maxRedirects {
			maxRedirects = n
		}
	}
	doer := h.doer()

	current := raw
	for hop := 0; ; hop++ {
		if hop > maxRedirects {
			h.fail(core.Err(core.E_RESOLVE_FAILED, "redirect limit %d exceeded", maxRedirects))
			return goja.Undefined()
		}
		u, err := url.Parse(current)
		if err != nil || u.Hostname() == "" {
			h.fail(core.Err(core.E_RESOLVE_SSRF_BLOCKED, "invalid request URL"))
			return goja.Undefined()
		}
		scheme := strings.ToLower(u.Scheme)
		if scheme != "http" && scheme != "https" {
			h.fail(core.Err(core.E_RESOLVE_SSRF_BLOCKED, "scheme %q is not allowed", scheme))
			return goja.Undefined()
		}
		if !HostAllowed(m.Capabilities.HTTP.AllowedHosts, u.Hostname()) {
			h.fail(core.Err(core.E_RESOLVE_SSRF_BLOCKED,
				"host %q is not in the plugin allowedHosts whitelist", u.Hostname()))
			return goja.Undefined()
		}
		if g := h.runtime.engine.Guard; g != nil {
			if err := g.Check(h.ctx, current); err != nil {
				h.fail(core.AsError(err))
				return goja.Undefined()
			}
		}

		req, err := http.NewRequestWithContext(h.ctx, http.MethodGet, current, nil)
		if err != nil {
			h.fail(core.Err(core.E_INVALID_INPUT, "build request: %v", err))
			return goja.Undefined()
		}
		for k, v := range headers {
			if hop > 0 && sensitiveHeaders[strings.ToLower(k)] {
				continue
			}
			req.Header.Set(k, v)
		}
		resp, err := doer.Do(req)
		if err != nil {
			h.fail(core.Err(core.E_TRANSIENT_NETWORK, "plugin request failed: %v", err))
			return goja.Undefined()
		}
		if isRedirect(resp.StatusCode) {
			loc := resp.Header.Get("Location")
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if loc == "" {
				break
			}
			next, err := u.Parse(loc)
			if err != nil {
				h.fail(core.Err(core.E_RESOLVE_FAILED, "invalid redirect Location"))
				return goja.Undefined()
			}
			current = next.String()
			continue
		}
		return h.readResponse(resp)
	}
	h.fail(core.Err(core.E_RESOLVE_FAILED, "redirect chain did not terminate"))
	return goja.Undefined()
}

func (h *hostState) readResponse(resp *http.Response) goja.Value {
	defer resp.Body.Close()
	if resp.ContentLength > h.maxBody {
		h.fail(core.Err(core.E_INVALID_INPUT,
			"response Content-Length %d exceeds maxResponseBytes %d", resp.ContentLength, h.maxBody))
		return goja.Undefined()
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, h.maxBody+1))
	if err != nil {
		h.fail(core.Err(core.E_TRANSIENT_NETWORK, "read plugin response: %v", err))
		return goja.Undefined()
	}
	if int64(len(data)) > h.maxBody {
		h.fail(core.Err(core.E_INVALID_INPUT,
			"response exceeds maxResponseBytes %d", h.maxBody))
		return goja.Undefined()
	}
	if !h.spend(int64(len(data))) {
		h.fail(core.Err(core.E_INVALID_INPUT, "plugin memory budget exceeded"))
		return goja.Undefined()
	}
	outHeaders := make(map[string]string)
	for k := range resp.Header {
		if len(outHeaders) >= 64 {
			break
		}
		outHeaders[http.CanonicalHeaderKey(k)] = truncate(resp.Header.Get(k), 2048)
	}
	obj := h.vm.NewObject()
	_ = obj.Set("status", resp.StatusCode)
	_ = obj.Set("body", string(data))
	_ = obj.Set("headers", outHeaders)
	return obj
}

func (h *hostState) doer() Doer {
	if d := h.runtime.engine.Doer; d != nil {
		return d
	}
	timeout := time.Duration(h.runtime.manifest.Limits.TimeoutMs) * time.Millisecond
	var client *http.Client
	if p, ok := h.runtime.engine.Guard.(httpClientProvider); ok {
		client = p.NewHTTPClient(timeout)
	} else {
		client = &http.Client{Timeout: timeout}
		if g := h.runtime.engine.Guard; g != nil {
			client.Transport = &http.Transport{DialContext: g.DialContext}
		}
	}
	// Redirects are followed manually by httpGet so every hop is checked.
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client
}

func parseHeaders(h *hostState, call goja.FunctionCall) map[string]string {
	out := map[string]string{}
	if len(call.Arguments) < 2 || goja.IsUndefined(call.Arguments[1]) || goja.IsNull(call.Arguments[1]) {
		return out
	}
	opts := call.Arguments[1].ToObject(h.vm)
	v := opts.Get("headers")
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return out
	}
	for _, k := range v.ToObject(h.vm).Keys() {
		if len(out) >= 32 {
			h.fail(core.Err(core.E_INVALID_INPUT, "too many request headers"))
			return nil
		}
		lower := strings.ToLower(k)
		if sensitiveHeaders[lower] {
			h.fail(core.Err(core.E_RESOLVE_SSRF_BLOCKED,
				"plugin must not set header %q (credentials / host override forbidden)", k))
			return nil
		}
		val := v.ToObject(h.vm).Get(k).String()
		if len(k) > 256 || len(val) > 2048 || strings.ContainsAny(k+val, "\r\n\x00") {
			h.fail(core.Err(core.E_INVALID_INPUT, "invalid request header"))
			return nil
		}
		out[k] = val
	}
	return out
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}
