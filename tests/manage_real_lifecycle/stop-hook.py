#!/usr/bin/python3
import os
import pathlib
import sys

marker = pathlib.Path(os.environ["MEMA_QUAL_CONTROL"]) / "fail-stop"
if marker.exists():
    print("injected ExecStop refusal", file=sys.stderr, flush=True)
    sys.exit(23)
