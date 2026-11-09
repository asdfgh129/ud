package security

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/asdfgh129/ud/internal/core"
)

var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// PathGuard validates relative output paths (docs/11 §4).
type PathGuard struct{}

// Clean resolves rel under root and rejects traversal, absolute paths,
// symlink escapes, Windows reserved names and overlong paths.
func (PathGuard) Clean(root, rel string) (string, error) {
	if rel == "" || strings.ContainsRune(rel, '\x00') {
		return "", core.Err(core.E_RESOLVE_FAILED, "empty or invalid output path")
	}
	if len(rel) > 4096 {
		return "", core.Err(core.E_RESOLVE_FAILED, "output path too long")
	}
	slash := filepath.ToSlash(rel)
	if filepath.IsAbs(slash) || strings.HasPrefix(slash, "/") || strings.HasPrefix(slash, "\\") {
		return "", core.Err(core.E_RESOLVE_FAILED, "absolute output path is forbidden")
	}
	if !filepath.IsLocal(filepath.FromSlash(slash)) {
		return "", core.Err(core.E_RESOLVE_FAILED, "output path escapes its directory (..)")
	}
	for _, seg := range strings.Split(slash, "/") {
		base := strings.ToUpper(strings.Split(seg, ".")[0])
		if windowsReserved[base] {
			return "", core.Err(core.E_RESOLVE_FAILED, "reserved device name %q", seg)
		}
		if filepath.VolumeName(seg) != "" {
			return "", core.Err(core.E_RESOLVE_FAILED, "volume name in path is forbidden")
		}
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(absRoot, filepath.FromSlash(slash))
	relCheck, err := filepath.Rel(absRoot, abs)
	if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		return "", core.Err(core.E_RESOLVE_FAILED, "output path escapes root")
	}
	// Symlink escape: compare resolved paths on BOTH sides. On macOS /var is a
	// symlink to /private/var, so comparing a resolved parent against an
	// unresolved root would produce a false positive for every temp dir.
	realRoot := absRoot
	if r, err := filepath.EvalSymlinks(absRoot); err == nil {
		realRoot = r
	}
	parent := filepath.Dir(abs)
	if resolved, err := filepath.EvalSymlinks(parent); err == nil {
		r, err := filepath.Rel(realRoot, resolved)
		if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return "", core.Err(core.E_RESOLVE_FAILED, "symlink escape in output directory")
		}
	}
	return abs, nil
}

// EnsureDir creates a directory with 0755 (files themselves use 0600).
func EnsureDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

// TempPath returns the .part path for a final output path.
func TempPath(final, suffix string) string {
	if suffix == "" {
		suffix = ".part"
	}
	return final + suffix
}
