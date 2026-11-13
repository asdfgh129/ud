#!/usr/bin/env python3
"""Real libtorrent 2.x seeder used by cmd/udinteropx (P1 WBS 11).

This is an OPTIONAL external tool, not a project dependency: it requires the
``libtorrent`` Python binding installed outside the repository (for example
``python3 -m pip install --target /tmp/udlt libtorrent``). CI never runs it and
the Go build has no knowledge of it. Without the binding this script reports
UNSUPPORTED instead of faking a result.

Wire protocol with cmd/udinteropx:

* argv: ``--torrent <file> --seed-dir <dir>`` (the Go harness writes both)
* stdout: exactly one JSON line, then keep seeding until stdin is closed:
  ``{"status":"READY","port":<int>,"engine":"libtorrent <version>",...}``
  or ``{"status":"UNSUPPORTED","reason":"..."}``
* stderr: free-form diagnostics (captured by the Go harness).

The seeder deliberately disables DHT / LSD / UPnP / NAT-PMP / PEX and only
listens on 127.0.0.1, so the evidence stays loopback-only and reproducible.
"""

import argparse
import json
import sys
import time
import warnings


def emit(obj):
    sys.stdout.write(json.dumps(obj, separators=(",", ":")) + "\n")
    sys.stdout.flush()


def wait_seeding(handle, timeout=10.0):
    deadline = time.time() + timeout
    while time.time() < deadline:
        status = handle.status()
        # add_torrent_params defaults to paused + auto_managed in libtorrent
        # 2.x. A paused torrent makes the session reject incoming connections
        # with "no active torrents", so both flags must be cleared and the
        # handle must be actively seeding before we report READY.
        if status.is_seeding and not status.paused:
            return status
        time.sleep(0.05)
    return None


def main():
    parser = argparse.ArgumentParser(description="libtorrent loopback seeder")
    parser.add_argument("--torrent", required=True)
    parser.add_argument("--seed-dir", required=True)
    args = parser.parse_args()

    try:
        import libtorrent as lt
    except Exception as exc:  # optional dependency: never claim a PASS here
        emit({"status": "UNSUPPORTED", "reason": "python libtorrent binding unavailable: %s" % exc})
        return 0

    try:
        info = lt.torrent_info(args.torrent)
        settings = {
            "listen_interfaces": "127.0.0.1:0",
            "enable_dht": False,
            "enable_lsd": False,
            "enable_upnp": False,
            "enable_natpmp": False,
            "announce_to_all_trackers": True,
        }
        session = lt.session(settings)
        params = lt.add_torrent_params()
        params.ti = info
        params.save_path = args.seed_dir
        params.flags |= (
            lt.torrent_flags.seed_mode
            | lt.torrent_flags.upload_mode
            | lt.torrent_flags.disable_pex
        )
        params.flags &= ~lt.torrent_flags.paused
        params.flags &= ~lt.torrent_flags.auto_managed
        handle = session.add_torrent(params)
        status = wait_seeding(handle)
        if status is None:
            s = handle.status()
            emit({"status": "FAIL", "reason": "libtorrent did not reach seeding: state=%s err=%s" % (s.state, s.errc)})
            return 1

        infohash = ""
        try:
            infohash = info.info_hashes().v1.to_string().hex()
        except Exception:
            pass
        peer_id = ""
        try:
            with warnings.catch_warnings():
                warnings.simplefilter("ignore", DeprecationWarning)
                raw = session.id()
            peer_id = raw.to_bytes(20, "big").hex() if isinstance(raw, int) else str(raw)
        except Exception:
            pass

        emit({
            "status": "READY",
            "port": session.listen_port(),
            "engine": "libtorrent " + lt.__version__,
            "infohash": infohash,
            "peer_id": peer_id,
            "torrent_name": status.name,
            "torrent_state": str(status.state),
            "dht": False,
            "lsd": False,
            "pex": False,
            "listen": "127.0.0.1",
        })
        # Keep the session alive until the Go harness closes stdin. Reading
        # until EOF avoids a busy loop and guarantees a deterministic cleanup.
        try:
            sys.stdin.buffer.read()
        except Exception:
            pass
        return 0
    except Exception as exc:
        emit({"status": "FAIL", "reason": "libtorrent seeder failed: %s: %s" % (type(exc).__name__, exc)})
        return 1


if __name__ == "__main__":
    sys.exit(main())
