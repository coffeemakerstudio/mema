#!/usr/bin/python3
import hashlib
import json
import os
import pathlib
import stat
import sys

root = pathlib.Path(sys.argv[1])
result = {}
for path in sorted(root.rglob("*")):
    rel = path.relative_to(root).as_posix()
    if rel in {"runtime/service.pid", "runtime/cache.tmp"}:
        continue
    info = path.lstat()
    st = info.st_uid, info.st_gid
    common = {"mode": stat.S_IMODE(info.st_mode), "uid": st[0], "gid": st[1]}
    if stat.S_ISLNK(info.st_mode):
        result[rel] = {**common, "kind": "symlink", "target": os.readlink(path)}
    elif stat.S_ISDIR(info.st_mode):
        result[rel] = {**common, "kind": "directory"}
    elif stat.S_ISREG(info.st_mode):
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        result[rel] = {**common, "kind": "file", "size": info.st_size, "sha256": digest}
    else:
        result[rel] = {**common, "kind": "special"}
print(json.dumps(result, sort_keys=True, separators=(",", ":")))
