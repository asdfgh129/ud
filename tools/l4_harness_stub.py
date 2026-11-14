#!/usr/bin/env python3
"""L4 external-harness STUB — a wiring example, NOT interoperability evidence.

`cmd/udinterop --require-external --external-config ...` runs one command per
BT case and reads the last JSON object line from stdout as its verdict. This
stub exists only so `tools/l4_external_config.example.json` is executable and
the wiring can be demonstrated end-to-end WITHOUT pretending the L4 matrix ran.

It ALWAYS emits SKIPPED. It can never emit PASS. Any real lab must replace
these commands with harnesses that drive genuine qBittorrent / Transmission /
libtorrent sessions, a real chihaya / opentracker binary and packet capture.
SKIPPED / UNSUPPORTED are never counted as PASS by the release gate.
"""
from __future__ import annotations

import json
import os
import sys

case_id = os.environ.get("UD_INTEROP_CASE_ID", "UNKNOWN")
label = sys.argv[1] if len(sys.argv) > 1 else case_id

print(json.dumps({
    "status": "SKIPPED",
    "reason": (
        "STUB: 这是 tools/l4_external_config.example.json 的占位 harness，"
        f"未运行 {case_id}（{label}）的真实客户端会话；不得据此宣称 L4 通过。"
    ),
    "evidence": {"stub": True, "case": case_id, "l4_executed": False},
}, ensure_ascii=False))
