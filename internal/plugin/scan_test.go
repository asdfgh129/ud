package plugin

import (
	"strings"
	"testing"

	"github.com/asdfgh129/ud/internal/core"
)

func TestTransformRewritesESMSugar(t *testing.T) {
	wrapped, err := Transform(happyScript)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(wrapped, "export function") {
		t.Fatal("export was not rewritten")
	}
	if !strings.Contains(wrapped, "function resolve(url, ctx)") {
		t.Fatal("resolve declaration lost")
	}
	if !strings.Contains(wrapped, `"use strict"`) || !strings.Contains(wrapped, "__ud_plugin") {
		t.Fatal("strict IIFE wrapper missing")
	}
	_, err = Transform("export const meta = { a: 1 };\nexport function resolve() { return 1; }")
	if err != nil {
		t.Fatalf("export const must be rewritten to var: %v", err)
	}
}

func TestScanRejectsForbiddenSyntax(t *testing.T) {
	// Each snippet must be rejected at load time, BEFORE any code runs.
	cases := map[string]string{
		"async":          "export function resolve() { return async function() {}; }",
		"await":          "export function resolve() { return await 1; }",
		"import":         "import x from 'y'; export function resolve() {}",
		"require":        "export function resolve() { return require('fs'); }",
		"fetch":          "export function resolve() { return fetch('https://x'); }",
		"XMLHttpRequest": "export function resolve() { return new XMLHttpRequest(); }",
		"WebSocket":      "export function resolve() { return new WebSocket('wss://x'); }",
		"process":        "export function resolve() { return process.env; }",
		"Deno":           "export function resolve() { return Deno.readTextFile('x'); }",
		"globalThis":     "export function resolve() { return globalThis; }",
		"eval":           "export function resolve() { return eval('1'); }",
		"Function":       "export function resolve() { return new Function('return this')(); }",
		"constructor":    "export function resolve() { return ({}).constructor.constructor('return this')(); }",
		"proto":          "export function resolve() { return ({})['__proto__']; }",
		"const":          "export function resolve() { const x = 1; return x; }",
		"let":            "export function resolve() { let x = 1; return x; }",
		"class":          "export function resolve() { class A {}; return A; }",
		"Symbol":         "export function resolve() { return Symbol('x'); }",
		"arrow":          "export function resolve() { var f = (x) => x; return f(1); }",
		"optional chain": "export function resolve() { return ctx.http?.get; }",
		"spread":         "export function resolve() { return [ ...[1,2] ]; }",
		"template":       "export function resolve() { return `hi ${1}`; }",
		"for of":         "export function resolve() { for (var x of [1]) { return x; } }",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Transform(src); err == nil {
				t.Fatalf("forbidden syntax %q was accepted", name)
			} else if got := core.AsError(err).Code; got != core.E_INVALID_INPUT {
				t.Fatalf("code=%s want %s", got, core.E_INVALID_INPUT)
			}
		})
	}
}

func TestScanIgnoresStringsCommentsAndRegexes(t *testing.T) {
	src := "export function resolve(url, ctx) {\n" +
		"  // fetch( and process.env inside a comment are fine\n" +
		"  /* const x = 1; await y; */\n" +
		"  var s = \"fetch('https://example.com') and async\";\n" +
		"  var re = /\"playUrl\":\"([^\"]+)\"/;\n" +
		"  return { url: url, s: s, ok: re.test(s) };\n" +
		"}\n"
	if err := Scan(src); err != nil {
		t.Fatalf("quoted/commented/regex content triggered a false positive: %v", err)
	}
	code := blankLiterals(src)
	if strings.Contains(code, "fetch(") {
		t.Fatal("string literal was not blanked")
	}
	if !strings.Contains(code, "function resolve") {
		t.Fatal("real code was blanked")
	}
}

func TestTransformRejectsUnsupportedExportAndOversize(t *testing.T) {
	if _, err := Transform("export default function() {}"); err == nil {
		t.Fatal("export default must be rejected")
	}
	if _, err := Transform("export { resolve };"); err == nil {
		t.Fatal("export list must be rejected")
	}
	if _, err := Transform(strings.Repeat("x", maxScriptBytes+1)); err == nil {
		t.Fatal("oversize script must be rejected")
	}
	if got := describe(nil); got != "OK" {
		t.Fatalf("describe(nil)=%q", got)
	}
	if got := describe(core.Err(core.E_INVALID_INPUT, "x")); got == "OK" {
		t.Fatal("describe(err) must not be OK")
	}
}

func TestCompileRejectsBrokenJS(t *testing.T) {
	m := baseManifest()
	if _, err := Compile(&m, []byte("function resolve( {"), Engine{}); err == nil {
		t.Fatal("syntax error must be rejected at compile time")
	}
	if _, err := Compile(&m, []byte("var meta = 1;"), Engine{}); err != nil {
		t.Fatalf("a script without resolve compiles but fails at call time: %v", err)
	}
}
