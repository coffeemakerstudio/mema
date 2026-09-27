#!/usr/bin/python3
"""Small deterministic MEMA lifecycle qualification service."""
import http.server
import json
import os
import pathlib
import signal
import socketserver
import sqlite3
import sys

DATA = pathlib.Path(os.environ["MEMA_QUAL_DATA"])
CONTROL = pathlib.Path(os.environ["MEMA_QUAL_CONTROL"])
RUNTIME = DATA / "runtime"


def state():
    document = json.loads((DATA / "application.json").read_text())
    with sqlite3.connect(DATA / "state.sqlite3") as db:
        integrity = db.execute("PRAGMA integrity_check").fetchone()[0]
        revision, count = db.execute("SELECT revision, item_count FROM service_state").fetchone()
        records = [row[0] for row in db.execute("SELECT value FROM records ORDER BY ordinal")]
    release = (DATA / "current" / "release.txt").read_text().strip()
    normal = (DATA / "nested" / "normal.txt").read_text().strip()
    return {
        "revision": document["revision"], "count": document["count"],
        "db_revision": revision, "db_count": count, "records": records,
        "release": release, "normal": normal, "integrity": integrity,
    }


def fail_marker(name, revision):
    marker = CONTROL / name
    try:
        return marker.read_text().strip() == revision
    except FileNotFoundError:
        return False


initial = state()
if fail_marker("fail-start", initial["revision"]):
    print("injected fixture start refusal", file=sys.stderr, flush=True)
    sys.exit(42)
RUNTIME.mkdir(mode=0o750, parents=True, exist_ok=True)
(RUNTIME / "service.pid").write_text(f"{os.getpid()}\n")
(RUNTIME / "cache.tmp").write_text("runtime-generated; never restore\n")
os.chmod(RUNTIME / "service.pid", 0o600)
os.chmod(RUNTIME / "cache.tmp", 0o600)


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        try:
            current = state()
            ready = current["integrity"] == "ok" and current["revision"] == current["db_revision"]
            if fail_marker("fail-health", current["revision"]):
                ready = False
            if self.path == "/ready":
                self.respond(200 if ready else 503, {"ready": ready})
            elif self.path == "/version":
                self.respond(200, {"version": "qualification-1", "revision": current["revision"]})
            elif self.path == "/state":
                self.respond(200, current)
            elif self.path == "/nginx-health":
                self.respond(404, {"error": "this endpoint must be served by nginx"})
            else:
                self.respond(404, {"error": "not found"})
        except Exception as exc:  # make fixture failures observable as health failures
            self.respond(503, {"error": str(exc)})

    def respond(self, status, value):
        body = json.dumps(value, sort_keys=True).encode() + b"\n"
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        print("fixture-app:", fmt % args, file=sys.stderr, flush=True)


class Server(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
    allow_reuse_address = True


def handle_term(_signum, _frame):
    if (CONTROL / "ignore-term").exists():
        print("injected SIGTERM refusal", file=sys.stderr, flush=True)
        return
    raise SystemExit(0)


signal.signal(signal.SIGTERM, handle_term)
server = Server(("127.0.0.1", int(os.environ["MEMA_QUAL_BACKEND_PORT"])), Handler)
print("qualification fixture listening", flush=True)
server.serve_forever(poll_interval=0.1)
server.server_close()
