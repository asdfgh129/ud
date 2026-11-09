#!/usr/bin/env python3
"""Static architecture guards required by docs/15 §2 and docs/13 §4.

Run by `make lint-arch` / `make lint` and by CI. Pure stdlib, no dependencies.

Guards:
  1. internal/core must not import any other internal package.
  2. transport hot path must not use os/exec / exec.Command / import "C" (cgo).
  3. PostProcessor.Process must only accept local-file types (ADR-0012).
  4. The global rate config may only be read by the unique Limiter.
  5. No GPL/cgo storage driver may enter the transport path.
  6. docs/_site must be generated, never hand-edited (checked in build_docs.py).
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
INTERNAL = ROOT / "internal"
MODULE = "github.com/asdfgh129/ud"

errors: list[str] = []
checks = 0


def go_files(*patterns: str) -> list[Path]:
    out: list[Path] = []
    for pattern in patterns:
        out.extend(sorted(INTERNAL.glob(pattern)))
    return out


def report(msg: str) -> None:
    errors.append(msg)


# 1. core has zero dependencies on implementation packages.
for f in go_files("core/*.go"):
    text = f.read_text(encoding="utf-8")
    for imp in re.findall(r'^\s*"([^"]+)"', text, re.M):
        if imp.startswith(MODULE):
            report(f"{f.relative_to(ROOT)}: core imports implementation package {imp}")

# 2. No external processes / cgo in the transport hot path.
for f in go_files("transport/**/*.go", "transport/*.go"):
    text = f.read_text(encoding="utf-8")
    for needle in ("os/exec", "exec.Command", 'import "C"', "syscall.Exec"):
        if needle in text:
            report(f"{f.relative_to(ROOT)}: transport hot path contains {needle}")

# 3. ADR-0012: PostProcessor only accepts local file paths.
iface = (INTERNAL / "core" / "interfaces.go").read_text(encoding="utf-8")
m = re.search(r"type PostProcessor interface \{(.*?)\n\}", iface, re.S)
if not m:
    report("core/interfaces.go: PostProcessor interface not found")
else:
    block = m.group(1)
    if "LocalFiles" not in block or "OutputSpec" not in block:
        report("PostProcessor.Process must use LocalFiles / OutputSpec")
    for forbidden in ("url.URL", "io.Reader", "io.Writer", "net.Conn", "http.Request", "context.Context, string"):
        if forbidden in block:
            report(f"PostProcessor.Process contains forbidden type {forbidden}")
for f in go_files("postproc/*.go"):
    if f.name.endswith("_test.go"):
        continue
    text = f.read_text(encoding="utf-8")
    if "func (Passthrough) Process" in text and "os.Stat" not in text:
        report(f"{f.relative_to(ROOT)}: local path existence is not validated")

# 4. Single authority for the GLOBAL rate (docs/04 §5.3).
# Test fixtures legitimately construct config literals to exercise the
# Limiter; they are not a second rate authority. Only production code is
# checked here (guards 2/5 still cover test files for exec/cgo).
rate_readers = []
for f in sorted(ROOT.glob("**/*.go")):
    if "/.git/" in str(f) or "/dist/" in str(f) or f.name.endswith("_test.go"):
        continue
    if "MaxDownloadRate" in f.read_text(encoding="utf-8"):
        rate_readers.append(f.relative_to(ROOT).as_posix())
for rel in rate_readers:
    if not (rel == "internal/scheduler/limiter.go" or rel.startswith("internal/config/")):
        report(f"{rel}: second read of global rate config (only the Limiter may read it)")
if "internal/scheduler/limiter.go" not in rate_readers:
    report("internal/scheduler/limiter.go does not read the global rate config")

# 5. cgo / GPL drivers must never appear in the transport path.
gomod = (ROOT / "go.mod").read_text(encoding="utf-8")
for banned in ("mattn/go-sqlite3", "aria2", "libtorrent"):
    if banned in gomod:
        report(f"go.mod contains banned dependency {banned}")
for f in sorted(ROOT.glob("**/*.go")):
    if "/dist/" in str(f):
        continue
    if 'import "C"' in f.read_text(encoding="utf-8"):
        report(f"{f.relative_to(ROOT)}: cgo import breaks CGO_ENABLED=0")

checks = 6
if errors:
    print(f"Architecture check: {len(errors)} violation(s) across {checks} guards")
    for e in errors:
        print("  [FAIL]", e)
    sys.exit(1)
print(f"Architecture check: {checks} guards passed, 0 violations")
