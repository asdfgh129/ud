package plugin

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/asdfgh129/ud/internal/core"
)

// goja is ES5.1-only by project policy (ADR-0014 §2). A rule pack may use the
// ESM-shaped `export const meta` / `export function resolve` sugar shown in
// docs/08 §3; the host rewrites that to ES5 `var` / `function` declarations
// inside a strict IIFE. Everything else modern (or capability-escaping) is
// rejected at load time, BEFORE the script is ever compiled or run.
var (
	exportDeclRE = regexp.MustCompile(`(?m)^([ \t]*)export[ \t]+(const|let|var|function|class)\b`)
	exportAnyRE  = regexp.MustCompile(`\bexport\b`)
	// Prototype-chain members are checked against the RAW source, not the
	// blanked one: obj['__proto__'] or obj['constructor'] would otherwise hide
	// inside a string literal while still escaping the sandbox.
	dangerousMemberRE = regexp.MustCompile(`__proto__|\bconstructor\b`)

	forbiddenTokens = []struct {
		re     *regexp.Regexp
		reason string
	}{
		{regexp.MustCompile(`\basync\b`), "async functions (ES2017)"},
		{regexp.MustCompile(`\bawait\b`), "await (ES2017)"},
		{regexp.MustCompile(`\bimport\b`), "ES module imports"},
		{regexp.MustCompile(`\brequire\s*\(`), "CommonJS require()"},
		{regexp.MustCompile(`\bfetch\s*\(`), "fetch() — use ctx.http.get()"},
		{regexp.MustCompile(`\bXMLHttpRequest\b`), "XMLHttpRequest"},
		{regexp.MustCompile(`\bWebSocket\b`), "WebSocket"},
		{regexp.MustCompile(`\bprocess\b`), "process / environment access"},
		{regexp.MustCompile(`\bDeno\b`), "Deno global"},
		{regexp.MustCompile(`\bglobalThis\b`), "globalThis sandbox escape"},
		{regexp.MustCompile(`\beval\s*\(`), "eval()"},
		{regexp.MustCompile(`\bFunction\s*\(|\bnew\s+Function\b`), "Function() sandbox escape"},
		{regexp.MustCompile(`\b__proto__\b|\bconstructor\b`), "prototype / constructor sandbox escape"},
		{regexp.MustCompile(`\bconst\b|\blet\b|\bclass\b`), "ES2015 declarations (use var)"},
		{regexp.MustCompile(`\bSymbol\b|\bProxy\b|\bReflect\b`), "ES2015 globals"},
		{regexp.MustCompile(`=>`), "arrow functions"},
		{regexp.MustCompile(`\?\.`), "optional chaining"},
		{regexp.MustCompile(`\.\.\.`), "spread / rest syntax"},
		{regexp.MustCompile("`"), "template literals"},
		{regexp.MustCompile(`\bfor\s*\([^)]*\bof\b`), "for..of"},
	}
)

// Transform rewrites the docs/08 ESM sugar and returns the ES5.1 wrapper that
// exposes exactly two values to the host: meta and resolve. The IIFE runs in
// strict mode so `this` is undefined and the plugin never sees the global
// object directly.
func Transform(src string) (string, error) {
	if len(src) > maxScriptBytes {
		return "", core.Err(core.E_INVALID_INPUT, "script exceeds %d bytes", maxScriptBytes)
	}
	// normalize CRLF to keep the line-anchored export rewrite predictable
	body := strings.ReplaceAll(src, "\r\n", "\n")
	body = exportDeclRE.ReplaceAllStringFunc(body, func(s string) string {
		m := exportDeclRE.FindStringSubmatch(s)
		kw := m[2]
		if kw == "const" || kw == "let" || kw == "class" {
			kw = "var"
		}
		return m[1] + kw
	})
	code := blankLiterals(body)
	if exportAnyRE.MatchString(code) {
		return "", core.Err(core.E_INVALID_INPUT,
			"unsupported export syntax; only `export const|var|function` declarations are rewritten")
	}
	if err := scanCode(body, code); err != nil {
		return "", err
	}
	wrapped := "var __ud_plugin = (function () {\n\"use strict\";\n" + body +
		"\nreturn {\n" +
		"  meta: (typeof meta === \"object\" && meta !== null) ? meta : {},\n" +
		"  resolve: (typeof resolve === \"function\") ? resolve : null\n" +
		"};\n})();\n"
	return wrapped, nil
}

// Scan rejects forbidden syntax after comments, string literals and regex
// literals have been blanked out, so a URL or a quoted "fetch(" cannot trigger
// a false positive.
func Scan(src string) error {
	return scanCode(src, blankLiterals(src))
}

func scanCode(src, code string) error {
	if loc := dangerousMemberRE.FindStringIndex(src); loc != nil {
		line := 1 + strings.Count(src[:loc[0]], "\n")
		return core.Err(core.E_INVALID_INPUT,
			"forbidden plugin syntax at line %d: prototype / constructor sandbox escape", line)
	}
	for _, f := range forbiddenTokens {
		if loc := f.re.FindStringIndex(code); loc != nil {
			line := 1 + strings.Count(code[:loc[0]], "\n")
			return core.Err(core.E_INVALID_INPUT,
				"forbidden plugin syntax at line %d: %s", line, f.reason)
		}
	}
	return nil
}

// blankLiterals is a small lexer: it blanks line/block comments, quoted
// strings and regex literals while preserving newlines and all other source
// characters. A `/` starts a regex literal only when the previous significant
// character cannot end an expression (the standard JS heuristic).
func blankLiterals(src string) string {
	out := []byte(src)
	blank := func(i int) {
		if out[i] != '\n' && out[i] != '\r' {
			out[i] = ' '
		}
	}
	prevSignificant := byte(';') // start of input behaves like after a delimiter
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			blank(i)
			i++
			for i < len(src) && src[i] != '\n' {
				blank(i)
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			blank(i)
			i++
			blank(i)
			i++
			for i < len(src) {
				if src[i] == '*' && i+1 < len(src) && src[i+1] == '/' {
					blank(i)
					i++
					blank(i)
					i++
					break
				}
				blank(i)
				i++
			}
		case c == '/' && regexAllowed(prevSignificant):
			blank(i)
			i++
			inClass := false
			for i < len(src) {
				if src[i] == '\n' {
					break
				}
				if src[i] == '\\' && i+1 < len(src) {
					blank(i)
					i++
					blank(i)
					i++
					continue
				}
				if src[i] == '[' {
					inClass = true
				} else if src[i] == ']' {
					inClass = false
				} else if src[i] == '/' && !inClass {
					blank(i)
					i++
					break
				}
				blank(i)
				i++
			}
			prevSignificant = '/'
		case c == '\'' || c == '"':
			quote := c
			blank(i)
			i++
			for i < len(src) {
				if src[i] == '\\' && i+1 < len(src) {
					blank(i)
					i++
					blank(i)
					i++
					continue
				}
				if src[i] == quote {
					blank(i)
					i++
					break
				}
				blank(i)
				i++
			}
			prevSignificant = 'x'
		case c == '`':
			// Template literals are blanked here but still reported by Scan
			// via the backtick token check (we keep the delimiter visible).
			i++
		default:
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				prevSignificant = c
			}
			i++
		}
	}
	return string(out)
}

func regexAllowed(prev byte) bool {
	switch prev {
	case ';', '(', ',', '=', ':', '[', '!', '&', '|', '?', '{', '}', '+', '-', '*', '%', '<', '>', '~', '^', '\n', '\r':
		return true
	}
	return false
}

// describe is used by CLI error output without leaking script contents.
func describe(err error) string {
	if err == nil {
		return "OK"
	}
	return fmt.Sprintf("%v", err)
}
