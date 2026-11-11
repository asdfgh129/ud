#!/usr/bin/env python3
"""P0 milestone review gate evidence collector (docs/12 §8, docs/14 §8).

Pure standard library. Every gate writes a machine-readable artifact into
dist/ and returns a status of PASS / WARN / FAIL:

  cover      go test -coverprofile + per-package threshold report
  sbom       CycloneDX 1.6 SBOM from the *linked* module graph + go mod verify
  licenses   copyleft / unknown license gate over the transport path
  vuln       govulncheck (WARN when the tool or the vulnerability DB is absent)
  bench      loopback HTTP Range throughput regression baseline
  rollback   backup -> simulated bad upgrade -> restore -> crash recovery
  all        run everything and write dist/gates.json + dist/GATES.md

Usage:
    python3 tools/gates.py all [--strict]
    python3 tools/gates.py cover|sbom|licenses|vuln|bench|rollback

Design rule: a gate must never silently claim success. Missing tools or an
unreachable vulnerability database are reported as WARN/BLOCKED with a reason,
and --strict turns every WARN into a non-zero exit.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import uuid
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DIST = ROOT / "dist"
MODULE = "github.com/asdfgh129/ud"

# docs/12 §1/§8 (v0.3 统一口径): core / security >= 90%, 其余关键路径包
# >= 80%. P1 WBS 9 added the FTP / SFTP movers and their shared tputil helper
# to the gated set; a new transport must not ship below the same bar. Packages
# with no production logic (cmd/*, internal/version, internal/transport) are
# reported but not gated. WARN is a real gap, never a silent pass. P1 WBS 7
# added the BT / magnet Transport to the gated set; the resolver pair
# (internal/resolve) already covers the magnet / .torrent parsers.
THRESHOLDS = {
    "internal/core": 90.0,
    "internal/security": 90.0,
    "internal/store": 80.0,
    "internal/scheduler": 80.0,
    "internal/transport/http": 80.0,
    "internal/transport/ftp": 80.0,
    "internal/transport/sftp": 80.0,
    "internal/transport/bt": 80.0,
    "internal/transport/tputil": 80.0,
    "internal/api": 80.0,
    "internal/config": 80.0,
    "internal/postproc": 80.0,
    "internal/resolve": 80.0,
    "internal/app": 80.0,
    "internal/plugin": 80.0,
    "internal/interop": 80.0,
}

COPYLEFT = re.compile(
    r"GNU\s+(AFFERO|LESSER\s+)?GENERAL\s+PUBLIC\s+LICENSE|"
    r"\b(AGPL|LGPL|GPL)-?[23]|SSPL|SERVER\s+SIDE\s+PUBLIC|"
    r"BUSL|BUSINESS\s+SOURCE|COMMONS\s+CLAUSE|ELASTIC\s+LICENSE|"
    r"CREATIVE\s+COMMONS.*NON-?COMMERCIAL",
    re.I,
)

LICENSE_PATTERNS = [
    ("Apache-2.0", re.compile(r"Apache License\s*,?\s*Version 2\.0", re.I)),
    ("MIT", re.compile(r"\bMIT License\b|Permission is hereby granted, free of charge", re.I)),
    ("MPL-2.0", re.compile(r"Mozilla Public License Version 2\.0", re.I)),
    # A three-clause BSD text always contains the "Neither the name" clause;
    # without it the standard boilerplate is the two-clause variant.
    ("BSD-3-Clause", re.compile(r"BSD\s+3-?Clause|Redistribution and use in source and binary forms.*Neither the name", re.I | re.S)),
    ("BSD-2-Clause", re.compile(r"BSD\s+2-?Clause|Simplified BSD|Redistribution and use in source and binary forms", re.I)),
    ("ISC", re.compile(r"\bISC License\b|Permission to use, copy, modify, and(?:/or)? distribute", re.I)),
    ("Unlicense", re.compile(r"\bThe Unlicense\b|This is free and unencumbered software", re.I)),
    ("CC0-1.0", re.compile(r"CC0\s+1\.0|Creative Commons Zero", re.I)),
    ("GPL-family", COPYLEFT),
]


def run(cmd: list[str], *, check: bool = False, capture: bool = True) -> subprocess.CompletedProcess:
    env = os.environ.copy()
    env.setdefault("CGO_ENABLED", "0")
    env.setdefault("GOPROXY", "https://goproxy.cn,direct")
    # See Makefile: go.mod pins a newer toolchain than the local `go`; a
    # persistent GOSUMDB=off prevents GOTOOLCHAIN=auto from verifying it.
    env.setdefault("GOSUMDB", "sum.golang.org")
    cp = subprocess.run(cmd, cwd=ROOT, env=env, text=True,
                        stdout=subprocess.PIPE if capture else None,
                        stderr=subprocess.STDOUT if capture else None)
    if check and cp.returncode != 0:
        raise RuntimeError(f"command failed ({cp.returncode}): {' '.join(cmd)}\n{(cp.stdout or '')[-3000:]}")
    return cp


def now() -> str:
    return datetime.now(timezone.utc).astimezone().isoformat(timespec="seconds")


def write_json(name: str, data: object) -> Path:
    DIST.mkdir(parents=True, exist_ok=True)
    p = DIST / name
    p.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return p


def tail(text: str, n: int = 12) -> str:
    lines = (text or "").splitlines()
    return "\n".join(lines[-n:])


# --------------------------------------------------------------------------- cover

def gate_cover() -> dict:
    profile = DIST / "cover.out"
    cp = run(["go", "test", "./...", f"-coverprofile={profile}", "-count=1"])
    if cp.returncode != 0:
        return {"gate": "cover", "status": "FAIL", "reason": "go test failed",
                "detail": tail(cp.stdout, 20)}
    if not profile.exists():
        return {"gate": "cover", "status": "FAIL", "reason": "cover profile missing"}

    stats: dict[str, dict[str, int]] = {}
    for line in profile.read_text(encoding="utf-8").splitlines()[1:]:
        m = re.match(r"^(.+?):\d+\.\d+,\d+\.\d+ (\d+) (\d+)$", line)
        if not m:
            continue
        pkg = m.group(1).rsplit("/", 1)[0].removeprefix(MODULE + "/")
        stmts, count = int(m.group(2)), int(m.group(3))
        s = stats.setdefault(pkg, {"statements": 0, "covered": 0})
        s["statements"] += stmts
        if count > 0:
            s["covered"] += stmts

    packages = []
    below = []
    for pkg in sorted(stats):
        s = stats[pkg]
        pct = round(100.0 * s["covered"] / s["statements"], 1) if s["statements"] else 0.0
        threshold = THRESHOLDS.get(pkg)
        ok = threshold is None or pct >= threshold
        if not ok:
            below.append({"package": pkg, "coverage": pct, "threshold": threshold})
        packages.append({"package": pkg, "coverage": pct, "threshold": threshold,
                         "statements": s["statements"], "covered": s["covered"], "meets": ok})

    overall = round(100.0 * sum(s["covered"] for s in stats.values()) /
                    max(1, sum(s["statements"] for s in stats.values())), 1)
    data = {"generated_at": now(), "command": "go test ./... -coverprofile=dist/cover.out -count=1",
            "overall_percent": overall, "thresholds": THRESHOLDS,
            "below_threshold": below, "packages": packages}
    write_json("coverage.json", data)
    status = "PASS" if not below else "WARN"
    return {"gate": "cover", "status": status,
            "reason": f"overall {overall}%; {len(below)} package(s) below threshold" if below else "all gated packages meet thresholds",
            "artifact": "dist/coverage.json", "detail": tail(cp.stdout, 6)}


# --------------------------------------------------------------------------- sbom

def module_stream(paths: list[str]) -> list[dict]:
    """Metadata for the modules LINKED into the binary, not the whole graph.

    `go list -m -json all` walks every transitive dependency including the
    test-only dependencies of third-party libraries (testify, x/net, ...).
    Those never appear in the binary but still have to be downloadable, which
    makes the SBOM gate fail in an offline/limited-proxy build even when the
    actual artifact is complete. Asking for the linked modules one by one uses
    only what `go build` already put in the module cache.
    """
    out = []
    for path in paths:
        cp = run(["go", "list", "-m", "-json", path])
        if cp.returncode != 0:
            continue
        try:
            out.append(json.loads(cp.stdout))
        except json.JSONDecodeError:
            continue
    return out


def purl(path: str, version: str) -> str:
    return f"pkg:golang/{path}@{version}"


def h1_to_hex(h1: str) -> str | None:
    if not h1.startswith("h1:"):
        return None
    try:
        return base64.b64decode(h1[3:]).hex()
    except Exception:
        return None


def go_sum_hashes() -> dict[str, str]:
    out = {}
    p = ROOT / "go.sum"
    if not p.exists():
        return out
    for line in p.read_text(encoding="utf-8").splitlines():
        parts = line.split()
        if len(parts) == 3 and parts[2].startswith("h1:"):
            out[f"{parts[0]}@{parts[1]}"] = parts[2]
    return out


def linked_modules() -> tuple[list[dict], str]:
    bins = sorted(p for p in DIST.glob("udd-*")
                  if p.is_file() and p.suffix not in {".json", ".txt", ".md", ".out"})
    if not bins:
        # Self-contained: one pure-Go binary is enough to read the linked graph.
        run(["go", "build", "-trimpath", "-o", str(DIST / "udd-darwin-amd64"), "./cmd/udd"], check=True)
        bins = [DIST / "udd-darwin-amd64"]
    binary = next((b for b in bins if b.name == "udd-darwin-amd64"), bins[0])
    cp = run(["go", "version", "-m", str(binary)], check=True)
    (DIST / "build-metadata.txt").write_text(cp.stdout, encoding="utf-8")
    deps = []
    for line in cp.stdout.splitlines():
        f = line.strip().split("\t")
        if len(f) >= 4 and f[0] == "dep":
            deps.append({"path": f[1], "version": f[2], "h1": f[3]})
    return deps, binary.name


def detect_license(module_dir: str | None) -> tuple[str, str, str]:
    """Return (spdx, source_file, license_text)."""
    if not module_dir or not Path(module_dir).is_dir():
        return "NOASSERTION", "", ""
    d = Path(module_dir)
    files = sorted([p for p in d.iterdir()
                    if p.is_file() and re.match(r"(?i)^(LICEN[CS]E|COPYING|NOTICE)", p.name)])
    if not files:
        parent = d.parent
        files = sorted([p for p in parent.iterdir()
                        if p.is_file() and re.match(r"(?i)^(LICEN[CS]E|COPYING|NOTICE)", p.name)]) if parent.is_dir() else []
    text = ""
    src = ""
    for p in files:
        try:
            text = p.read_text(encoding="utf-8", errors="replace")[:40000]
        except OSError:
            continue
        src = str(p)
        spdx = re.search(r"SPDX-License-Identifier:\s*([^\s]+)", text)
        if spdx:
            return spdx.group(1), src, text
        if COPYLEFT.search(text):
            return "GPL-family", src, text
        for name, pat in LICENSE_PATTERNS:
            if pat.search(text):
                return name, src, text
        if len(text) > 200:
            break
    return "NOASSERTION", src, text


def build_sbom() -> dict:
    DIST.mkdir(parents=True, exist_ok=True)
    linked, binary = linked_modules()
    modules = module_stream([dep["path"] for dep in linked])
    (DIST / "module-graph.json").write_text(
        json.dumps(modules, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    by_path = {m.get("Path"): m for m in modules}
    # The SBOM describes whatever `go version -m` finds in dist/, while the
    # license text is read from the *current* module cache. After a dependency
    # bump, a stale binary would therefore emit an artifact mixing the old
    # version with the new license. Refuse instead of silently claiming
    # success (docs/12 §8 "never silently pass").
    stale = []
    for dep in linked:
        want = by_path.get(dep["path"], {}).get("Version")
        if want and want != dep["version"]:
            stale.append(f"{dep['path']}: binary {dep['version']} vs go.mod {want}")
    if stale:
        raise RuntimeError(
            "stale dist/ binary, run `make build` before `make gates` ("
            + "; ".join(stale) + ")")
    sums = go_sum_hashes()

    components = []
    for dep in linked:
        p, v = dep["path"], dep["version"]
        meta = by_path.get(p, {})
        spdx, src, text = detect_license(meta.get("Dir"))
        digest = h1_to_hex(dep.get("h1") or sums.get(f"{p}@{v}", ""))
        comp = {
            "type": "library", "bom-ref": purl(p, v), "name": p, "version": v,
            "purl": purl(p, v),
            "properties": [{"name": "ud:in-transport-path", "value": "true"}],
        }
        if digest:
            comp["hashes"] = [{"alg": "SHA-256", "content": digest,
                               "note": "decoded from the go.sum h1 dirhash"}]
        if spdx != "NOASSERTION":
            comp["licenses"] = [{"license": {"id": spdx}}]
        components.append(comp)
        dep["license"] = spdx
        dep["license_file"] = src
        dep["license_text"] = text

    bom = {
        "bomFormat": "CycloneDX", "specVersion": "1.6",
        "serialNumber": f"urn:uuid:{uuid.uuid4()}", "version": 1,
        "metadata": {
            "timestamp": now(),
            "tools": [{"vendor": "Universal Downloader", "name": "tools/gates.py", "version": "0.1.0"}],
            "component": {"type": "application", "bom-ref": purl(MODULE, "v0.1.0"),
                          "name": MODULE, "version": "v0.1.0"},
            "properties": [{"name": "ud:binary", "value": binary}],
        },
        "components": components,
    }
    write_json("sbom.cdx.json", bom)

    # Checksums for every produced binary (docs/12 §8 "SBOM 与 go version -m 一致").
    lines = []
    for p in sorted(DIST.glob("udd-*")) + sorted(DIST.glob("udctl-*")):
        if p.is_file() and p.suffix not in {".json", ".txt", ".md", ".out"}:
            lines.append(f"{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}")
    (DIST / "checksums.txt").write_text("\n".join(lines) + "\n", encoding="utf-8")

    verify = run(["go", "mod", "verify"])
    (DIST / "mod-verify.txt").write_text(verify.stdout or "", encoding="utf-8")

    notices = ["# THIRD PARTY NOTICES", "",
               f"Generated: {now()}", f"Binary: `{binary}` (linked module graph only).", "",
               "| Module | Version | License | Source |", "|---|---|---|---|"]
    for dep in linked:
        notices.append(f"| `{dep['path']}` | {dep['version']} | {dep['license']} | `{dep['license_file']}` |")
    notices += ["", "## License texts", ""]
    for dep in linked:
        notices += [f"### {dep['path']} {dep['version']} — {dep['license']}", "",
                    "```text", (dep["license_text"] or "(no license file found in module cache)").strip()[:8000], "```", ""]
    (DIST / "THIRD_PARTY_NOTICES.md").write_text("\n".join(notices), encoding="utf-8")

    return {"linked": linked, "binary": binary, "verify_rc": verify.returncode,
            "verify_output": (verify.stdout or "").strip()}


def gate_sbom() -> dict:
    try:
        info = build_sbom()
    except Exception as e:
        return {"gate": "sbom", "status": "FAIL", "reason": str(e)}
    if info["verify_rc"] != 0:
        return {"gate": "sbom", "status": "FAIL", "reason": "go mod verify failed",
                "detail": info["verify_output"]}
    return {"gate": "sbom", "status": "PASS",
            "reason": f"CycloneDX 1.6 with {len(info['linked'])} linked components; go mod verify OK",
            "artifacts": ["dist/sbom.cdx.json", "dist/THIRD_PARTY_NOTICES.md",
                          "dist/build-metadata.txt", "dist/checksums.txt"]}


def gate_licenses() -> dict:
    p = DIST / "sbom.cdx.json"
    if not p.exists():
        return {"gate": "licenses", "status": "FAIL", "reason": "SBOM missing; run `gates.py sbom`"}
    bom = json.loads(p.read_text(encoding="utf-8"))
    rows = []
    for c in bom["components"]:
        ids = [x["license"]["id"] for x in c.get("licenses", [])]
        spdx = ids[0] if ids else "NOASSERTION"
        rows.append({"module": c["name"], "version": c["version"], "license": spdx,
                     "blocked": bool(COPYLEFT.search(spdx)), "unknown": spdx == "NOASSERTION"})
    blocked = [r for r in rows if r["blocked"]]
    unknown = [r for r in rows if r["unknown"]]
    write_json("license-report.json", {"generated_at": now(), "components": rows,
                                       "blocked": blocked, "unknown": unknown})
    if blocked:
        return {"gate": "licenses", "status": "FAIL",
                "reason": "copyleft license in transport path: " + ", ".join(r["module"] for r in blocked)}
    if unknown:
        return {"gate": "licenses", "status": "WARN",
                "reason": "unidentified license(s): " + ", ".join(r["module"] for r in unknown)}
    return {"gate": "licenses", "status": "PASS",
            "reason": f"{len(rows)} linked modules, 0 copyleft, 0 unknown",
            "artifact": "dist/license-report.json"}


# --------------------------------------------------------------------------- vuln

def gate_vuln() -> dict:
    tool = os.environ.get("GOVULNCHECK") or shutil.which("govulncheck")
    if not tool or not Path(tool).exists():
        msg = ("govulncheck not installed; install with "
               "`go install golang.org/x/vuln/cmd/govulncheck@latest`")
        (DIST / "vuln.txt").write_text("BLOCKED: " + msg + "\n", encoding="utf-8")
        return {"gate": "vuln", "status": "WARN", "reason": msg}
    cp = run([tool, "./..."])
    (DIST / "vuln.txt").write_text(cp.stdout or "", encoding="utf-8")
    out = cp.stdout or ""
    if cp.returncode not in (0, 3) or "vulnerability database" in out.lower() and "error" in out.lower():
        return {"gate": "vuln", "status": "WARN",
                "reason": f"govulncheck could not complete (rc={cp.returncode})", "detail": tail(out, 8)}
    if "No vulnerabilities found" in out or cp.returncode == 0:
        return {"gate": "vuln", "status": "PASS", "reason": "no known vulnerabilities",
                "artifact": "dist/vuln.txt", "detail": tail(out, 4)}
    return {"gate": "vuln", "status": "FAIL", "reason": "vulnerabilities reported",
            "artifact": "dist/vuln.txt", "detail": tail(out, 12)}


# --------------------------------------------------------------------------- bench

def gate_bench() -> dict:
    cp = run(["go", "test", "./internal/transport/http", "-run", "^$",
              "-bench", "^BenchmarkRangeThroughput$", "-benchmem", "-count=1", "-benchtime=3x"])
    out = cp.stdout or ""
    (DIST / "bench.txt").write_text(out, encoding="utf-8")
    if cp.returncode != 0:
        return {"gate": "bench", "status": "FAIL", "reason": "benchmark failed", "detail": tail(out, 10)}
    m = re.search(r"BenchmarkRangeThroughput-\d+\s+\d+\s+([\d.]+) ns/op\s+([\d.]+) MB/s", out)
    if not m:
        return {"gate": "bench", "status": "WARN", "reason": "could not parse MB/s", "detail": tail(out, 8)}
    data = {"generated_at": now(), "scope": "P0 loopback microbenchmark (NOT NFR-PERF-01)",
            "workload": "32 MiB in-memory fixture, Range, 4 connections, 256 KiB pieces",
            "ns_per_op": float(m.group(1)), "MB_per_s": float(m.group(2))}
    write_json("bench.json", data)
    return {"gate": "bench", "status": "PASS",
            "reason": f"{data['MB_per_s']} MB/s loopback baseline (not the gigabit-LAN NFR gate)",
            "artifact": "dist/bench.json"}


# ------------------------------------------------------------------------ rollback

def gate_rollback() -> dict:
    cp = run(["go", "test", "./internal/store", "-run", "^TestBackupRestoreRollbackDrill$",
              "-count=1", "-v"])
    out = cp.stdout or ""
    (DIST / "rollback-drill.txt").write_text(out, encoding="utf-8")
    if cp.returncode != 0 or "--- PASS" not in out:
        return {"gate": "rollback", "status": "FAIL", "reason": "rollback drill failed",
                "detail": tail(out, 12)}
    return {"gate": "rollback", "status": "PASS",
            "reason": "backup -> simulated bad upgrade -> restore -> recovery reuses verified chunks",
            "artifact": "dist/rollback-drill.txt"}


# --------------------------------------------------------------------------- all

GATES = {
    "cover": gate_cover,
    "sbom": gate_sbom,
    "licenses": gate_licenses,
    "vuln": gate_vuln,
    "bench": gate_bench,
    "rollback": gate_rollback,
}

MARKDOWN = {
    "cover": "覆盖率阈值（12 §8）",
    "sbom": "CycloneDX SBOM + go mod verify（12 §8）",
    "licenses": "传输路径许可证扫描（NFR-LEG-01）",
    "vuln": "依赖漏洞扫描（12 §6）",
    "bench": "HTTP 吞吐回归基线（12 §5）",
    "rollback": "回滚/降级路径演练（12 §8 DoD）",
}


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("command", choices=[*GATES, "all"])
    ap.add_argument("--strict", action="store_true", help="treat WARN as failure")
    args = ap.parse_args()
    DIST.mkdir(parents=True, exist_ok=True)

    if args.command == "all":
        results = []
        sbom_failed = False
        for name, fn in GATES.items():
            if name == "licenses" and sbom_failed:
                # Never let a downstream gate claim success from an artifact
                # left over by an earlier run (docs/12 §8 "never silently pass").
                r = {"gate": "licenses", "status": "FAIL",
                     "reason": "SBOM could not be regenerated; refusing to scan a stale artifact"}
            else:
                r = fn()
            if r["gate"] == "sbom" and r["status"] == "FAIL":
                sbom_failed = True
            results.append(r)
            print(f"[{r['status']:4}] {name:9} {r['reason']}")
            if r.get("detail"):
                for line in r["detail"].splitlines():
                    print(f"         {line}")
        summary = {"generated_at": now(), "results": results,
                   "failed": [r["gate"] for r in results if r["status"] == "FAIL"],
                   "warned": [r["gate"] for r in results if r["status"] == "WARN"]}
        write_json("gates.json", summary)
        lines = ["# P0 里程碑评审 · 客观门禁执行结果", "",
                 f"生成时间：{now()} ｜ 命令：`python3 tools/gates.py all`", "",
                 "| 门禁 | 状态 | 结论 |", "|---|---|---|"]
        for r in results:
            lines.append(f"| {MARKDOWN[r['gate']]} | {r['status']} | {r['reason']} |")
        lines += ["", "> `WARN` 表示已采集证据但未达到发布门禁阈值，或工具/网络不可用；",
                  "> 它不是「通过」。全部证据文件位于 `dist/`（`.gitignore` 忽略）。", ""]
        (DIST / "GATES.md").write_text("\n".join(lines), encoding="utf-8")
        rc = 1 if summary["failed"] or (args.strict and summary["warned"]) else 0
        print(f"\nsummary: {sum(r['status'] == 'PASS' for r in results)} PASS / "
              f"{len(summary['warned'])} WARN / {len(summary['failed'])} FAIL  -> dist/gates.json")
        return rc

    r = GATES[args.command]()
    print(json.dumps(r, ensure_ascii=False, indent=2))
    return 0 if r["status"] == "PASS" or (r["status"] == "WARN" and not args.strict) else 1


if __name__ == "__main__":
    sys.exit(main())
