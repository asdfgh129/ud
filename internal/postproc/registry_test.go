package postproc

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/asdfgh129/ud/internal/core"
)

func TestRegistryRegistersPassthroughByDefault(t *testing.T) {
	r := NewRegistry()
	names := r.Names()
	sort.Strings(names)
	if len(names) != 1 || names[0] != "none" {
		t.Fatalf("default registry must expose exactly the passthrough processor, got %v", names)
	}
	for _, alias := range []string{"", "passthrough", "none"} {
		p, err := r.Get(alias)
		if err != nil {
			t.Fatalf("Get(%q): %v", alias, err)
		}
		if p.Name() != "none" {
			t.Fatalf("Get(%q) returned %q", alias, p.Name())
		}
	}
}

type namedProcessor struct{ Passthrough }

func (namedProcessor) Name() string { return "remux" }

func TestRegistryRegisterAddsProcessor(t *testing.T) {
	r := NewRegistry()
	r.Register(namedProcessor{})
	names := r.Names()
	sort.Strings(names)
	if len(names) != 2 || names[0] != "none" || names[1] != "remux" {
		t.Fatalf("unexpected names after register: %v", names)
	}
	if p, err := r.Get("remux"); err != nil || p.Name() != "remux" {
		t.Fatalf("Get(remux) = %v, %v", p, err)
	}
}

// ADR-0012: input must be an existing local file, never a URL or a directory.
func TestPassthroughRejectsUnreadableInputs(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.part")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name  string
		ctx   context.Context
		paths []string
	}{
		{"missing file", context.Background(), []string{filepath.Join(dir, "nope.part")}},
		{"directory", context.Background(), []string{dir}},
		{"cancelled context", cancelled, []string{file}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Passthrough{}.Process(tc.ctx, core.LocalFiles{Paths: tc.paths}, core.OutputSpec{})
			if err == nil || core.AsError(err).Code != core.E_POSTPROCESS_FAILED {
				t.Fatalf("want E_POSTPROCESS_FAILED, got %v", err)
			}
		})
	}
}

func TestPassthroughCopiesInputPaths(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.part")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := []string{file}
	res, err := Passthrough{}.Process(context.Background(),
		core.LocalFiles{Paths: in}, core.OutputSpec{})
	if err != nil {
		t.Fatal(err)
	}
	// The result must be an independent copy: the core may mutate its own slice.
	in[0] = "mutated"
	if res.Outputs[0] != file || res.TempPaths[0] != file {
		t.Fatalf("passthrough must copy paths, got %+v", res)
	}
}
