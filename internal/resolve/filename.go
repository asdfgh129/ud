package resolve

import (
	"net/url"
	"path"
	"strings"
)

// filenameFromURL derives a safe relative output name from an untrusted URL.
// It is shared by the direct, wrapper and Metalink resolvers so all three
// apply identical sanitisation (docs/11 §4, ADR-0007 §2).
func filenameFromURL(u *url.URL) string {
	name, err := url.PathUnescape(path.Base(u.EscapedPath()))
	if err != nil || name == "" || name == "/" || name == "." || name == ".." {
		return "download.bin"
	}
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == '\x00' {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == ".." {
		return "download.bin"
	}
	if len(name) > 200 {
		name = name[len(name)-200:]
	}
	return name
}

// filenameFromRaw parses raw first, then applies filenameFromURL.
func filenameFromRaw(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "download.bin"
	}
	return filenameFromURL(u)
}

// sanitizeName cleans a path supplied by an untrusted manifest (Metalink
// <file name="...">). It rejects traversal, absolute paths and control chars
// before any path reaches the store (docs/11 §4, FR-RSV-07).
func sanitizeName(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "\\", "/")
	s = path.Base(s)
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '\x00' {
			return -1
		}
		return r
	}, s)
	if s == "" || s == "." || s == ".." || s == "/" {
		return "download.bin"
	}
	if len(s) > 200 {
		s = s[len(s)-200:]
	}
	return s
}
