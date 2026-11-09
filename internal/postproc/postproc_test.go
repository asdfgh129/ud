package postproc

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/asdfgh129/ud/internal/core"
)

// T-FR-PST-03-01 / ADR-0012: the interface must never accept URL / io.Reader /
// network streams. Reflection guards the signature at runtime as well.
func TestPostProcessorSignature(t *testing.T) {
	typ := reflect.TypeOf((*core.PostProcessor)(nil)).Elem()
	m, ok := typ.MethodByName("Process")
	if !ok {
		t.Fatal("Process method missing")
	}
	for i := 1; i < m.Type.NumIn(); i++ {
		name := m.Type.In(i).String()
		for _, forbidden := range []string{"url.URL", "io.Reader", "io.Writer", "net.Conn", "http.Request"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("Process parameter %d (%s) contains forbidden type %s", i, name, forbidden)
			}
		}
	}
	if m.Type.NumOut() != 2 {
		t.Fatal("Process must return (*ProcessResult, error)")
	}
}

func TestPassthroughRejectsNonLocalInput(t *testing.T) {
	p := Passthrough{}
	for _, bad := range []core.LocalFiles{
		{Paths: []string{"https://example.com/video.ts"}},
		{Paths: []string{""}},
		{Paths: nil},
	} {
		if _, err := p.Process(context.Background(), bad, core.OutputSpec{Target: "out.mp4"}); err == nil {
			t.Fatalf("expected rejection for %+v", bad)
		}
	}
}

func TestPassthroughReturnsPartPathsForCoreCommit(t *testing.T) {
	dir := t.TempDir()
	part := filepath.Join(dir, "out.mp4.part")
	if err := os.WriteFile(part, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := Passthrough{}
	res, err := p.Process(context.Background(),
		core.LocalFiles{BaseDir: dir, Paths: []string{part}},
		core.OutputSpec{Target: filepath.Join(dir, "out.mp4"), TempSuffix: ".part"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Outputs) != 1 || res.Outputs[0] != part {
		t.Fatalf("bad outputs: %+v", res.Outputs)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.mp4")); !os.IsNotExist(err) {
		t.Fatal("plugin must not create the final file; the core owns the rename")
	}
}

func TestRegistryUnknownProcessorDegrades(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get("none"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("ffmpeg-transcode"); err == nil ||
		core.AsError(err).Code != core.E_CAPABILITY_UNAVAILABLE {
		t.Fatalf("expected capability degradation, got %v", err)
	}
}
