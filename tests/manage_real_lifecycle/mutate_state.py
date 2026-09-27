#!/usr/bin/python3
"""Create deterministic A/B fixture states while the service is running."""
import json
import os
import pathlib
import sqlite3
import sys

root = pathlib.Path(sys.argv[1])
revision = sys.argv[2]
count = 41 if revision == "A" else 900
records = ["alpha", "beta", "gamma"] if revision == "A" else ["delta", "epsilon"]
(root / "nested").mkdir(parents=True, exist_ok=True)
(root / "bin").mkdir(parents=True, exist_ok=True)
(root / "releases" / revision).mkdir(parents=True, exist_ok=True)
(root / "runtime").mkdir(parents=True, exist_ok=True)
(root / "application.json").write_text(json.dumps({"revision": revision, "count": count, "records": records}, sort_keys=True) + "\n")
(root / "nested" / "normal.txt").write_text(f"normal-file-{revision}\n")
(root / "nested" / "empty.dat").write_bytes(b"")
(root / "releases" / revision / "release.txt").write_text(f"release-{revision}\n")
os.chmod(root / "application.json", 0o640)
os.chmod(root / "nested" / "normal.txt", 0o644)
os.chmod(root / "nested" / "empty.dat", 0o600)
os.chmod(root / "releases" / revision / "release.txt", 0o644)
(root / "bin" / "run").write_text("#!/bin/sh\nexit 0\n")
os.chmod(root / "bin" / "run", 0o751 if revision == "A" else 0o700)
with sqlite3.connect(root / "state.sqlite3") as db:
    db.executescript("DROP TABLE IF EXISTS service_state; DROP TABLE IF EXISTS records;")
    db.execute("CREATE TABLE service_state(revision TEXT NOT NULL, item_count INTEGER NOT NULL)")
    db.execute("INSERT INTO service_state VALUES(?, ?)", (revision, count))
    db.execute("CREATE TABLE records(ordinal INTEGER NOT NULL, value TEXT NOT NULL)")
    db.executemany("INSERT INTO records VALUES(?, ?)", enumerate(records))
    db.commit()
os.chmod(root / "state.sqlite3", 0o600)
link = root / "current"
if link.is_symlink() or link.exists():
    link.unlink()
link.symlink_to(pathlib.Path("releases") / revision)
if revision == "B":
    (root / "stale-b-only.txt").write_text("must disappear when A is restored\n")
    (root / "b-only-directory").mkdir(exist_ok=True)
    (root / "b-only-directory" / "stale.txt").write_text("must also disappear\n")
