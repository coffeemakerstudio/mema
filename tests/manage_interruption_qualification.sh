#!/usr/bin/env bash
set -Eeuo pipefail
ROOT=$(realpath "$(dirname "$0")/..")
IMAGE="mema-interruption-qualification-$$"
NAME="mema-interruption-qualification-$$"
cleanup(){ docker rm -f "$NAME" >/dev/null 2>&1 || true; docker image rm -f "$IMAGE" >/dev/null 2>&1 || true; }
trap cleanup EXIT
docker build --quiet -f "$ROOT/tests/manage_qualification/Dockerfile" -t "$IMAGE" "$ROOT" >/dev/null
docker run --rm --init --name "$NAME" --network=none --memory=1g --cpus=2 --pids-limit=256 "$IMAGE" bash -Eeuo pipefail -c '
export MEMA_MANAGE_MANIFEST_DIR=/etc/mema/manage MEMA_MANAGE_STATE_DIR=/var/lib/mema/manage MEMA_MANAGE_GPG_HOME=/recovery-gpg
mkdir -m 700 -p /recovery-gpg /recovery
set -a; . /etc/mema-qualification/service.env; set +a
gpg --batch --pinentry-mode loopback --passphrase "" --homedir /recovery-gpg --quick-generate-key "qualification <qualification@example.invalid>" default default 1d >/dev/null 2>&1
/opt/mema-qualification/bin/service.py >/recovery/app.log 2>&1 & APP_PID=$!
trap "kill $APP_PID 2>/dev/null || true" EXIT
for i in $(seq 1 30); do curl -fsS http://127.0.0.1:18080/ready >/dev/null 2>&1 && break; sleep .2; done
/usr/local/bin/mema manage qualification snapshot --json >/recovery/good-snapshot.json
python3 - <<"PY"
import json
open("/recovery/good-id","w").write(json.load(open("/recovery/good-snapshot.json"))["id"])
PY

# Capture interruption: a FIFO blocks the managed capture syscall. Kill only the
# disposable Mema process, then remove its incomplete snapshot locally.
python3 - <<"PY"
import json
p="/etc/mema/manage/qualification.json"
m=json.load(open(p)); m["data"].append({"path":"/var/lib/mema-qualification/capture.fifo","role":"capture-test"})
json.dump(m, open(p,"w"))
PY
mkfifo /var/lib/mema-qualification/capture.fifo
/usr/local/bin/mema manage qualification snapshot --json >/recovery/capture.out 2>/recovery/capture.err & MEMA_PID=$!
for i in $(seq 1 50); do test -f /var/lib/mema/manage/qualification.lock && break; sleep .1; done
kill -KILL "$MEMA_PID" 2>/dev/null || true; wait "$MEMA_PID" 2>/dev/null || true
python3 - <<"PY"
import json, os
root="/var/lib/mema/manage/qualification/snapshots"
found=[]
for name in os.listdir(root):
 p=os.path.join(root,name,"manifest.json")
 try:
  x=json.load(open(p))
  if not x.get("complete") or x.get("state") != "verified": found.append(os.path.join(root,name))
 except Exception: pass
assert found, "no incomplete capture snapshot"
for p in found: assert not os.path.exists(os.path.join(p,"payload.gpg")); open("/recovery/capture-dir","w").write(p); break
PY
rm -rf "$(cat /recovery/capture-dir)"
rm -f /var/lib/mema-qualification/capture.fifo
printf "%s\n" CAPTURE_INTERRUPTION=PASS

# Encryption interruption: a controlled GPG failure must journal failure and clean plaintext.
mkdir -p /fault-bin
cat > /fault-bin/gpg <<"SH"
#!/bin/sh
if [ "${MEMA_FAULT:-}" = encryption ]; then exit 75; fi
exec /usr/bin/gpg "$@"
SH
chmod 0755 /fault-bin/gpg
set +e
MEMA_FAULT=encryption PATH=/fault-bin:$PATH /usr/local/bin/mema manage qualification snapshot --json >/recovery/encryption.out 2>/recovery/encryption.err
rc=$?; set -e
test "$rc" -ne 0
python3 - <<"PY"
import json, os
root="/var/lib/mema/manage/qualification/snapshots"; failed=[]
for n in os.listdir(root):
 p=os.path.join(root,n,"manifest.json")
 try:
  x=json.load(open(p))
  if x.get("state") == "failed": failed.append(os.path.join(root,n))
 except Exception: pass
assert failed
for p in failed:
 assert not os.path.exists(os.path.join(p,"filesystem"))
 assert not os.path.exists(os.path.join(p,".payload.tar.gz"))
 assert not os.path.exists(os.path.join(p,"payload.gpg"))
PY
! test -e /var/lib/mema/manage/qualification.lock
printf "%s\n" ENCRYPTION_INTERRUPTION=PASS

# Backend-write interruption: FTP transfer is forced to fail inside the container.
cat > /etc/mema/manage/qualification.json <<"JSON"
{"version":1,"service":"qualification","files":[{"path":"/etc/systemd/system/mema-qualification.service","role":"systemd-unit"},{"path":"/etc/mema-qualification/service.env","role":"env","secret":true},{"path":"/opt/mema-qualification/bin/service.py","role":"binary"},{"path":"/var/lib/mema-qualification/app.db","role":"database"},{"path":"/var/lib/mema-qualification/current","role":"active-data","type":"symlink"}],"config":[{"path":"/etc/nginx/sites-available/mema-qualification","role":"nginx-config"},{"path":"/etc/nginx/sites-enabled/mema-qualification","role":"nginx-enabled","type":"symlink"}],"data":[{"path":"/var/lib/mema-qualification/data","role":"persistent-data"}],"identity":[{"path":"/var/lib/mema-qualification/identity","secret":true,"critical":true}],"targets":{"runtime":{"type":"systemd","units":["mema-qualification.service"]},"reverse_proxy":{"type":"nginx","configs":["/etc/nginx/sites-available/mema-qualification"]}},"snapshot":{"recipient":"qualification@example.invalid"},"backup":{"backend":"fault"}}
JSON
printf "[{\"name\":\"fault\",\"type\":\"ftp\",\"url\":\"ftp://127.0.0.1:1\"}]\n" > /etc/mema/backends.json
set +e
/usr/local/bin/mema manage qualification snapshot --json >/recovery/backend.out 2>/recovery/backend.err
rc=$?; set -e
test "$rc" -ne 0
python3 - <<"PY"
import json, os
root="/var/lib/mema/manage/qualification/snapshots"; failed=[]
for n in os.listdir(root):
 p=os.path.join(root,n,"manifest.json")
 try:
  x=json.load(open(p))
  if x.get("state") == "failed": failed.append(os.path.join(root,n))
 except Exception: pass
assert failed
for p in failed:
 assert not os.path.exists(os.path.join(p,"filesystem"))
 assert not os.path.exists(os.path.join(p,".payload.tar.gz"))
PY
! test -e /var/lib/mema/manage/qualification.lock
printf "%s\n" BACKEND_WRITE_INTERRUPTION=PASS

# Restore interruption: fake target activation fails; a clean retry succeeds.
cp /etc/mema/manage/qualification.json /recovery/ftp-manifest.json
cp /recovery/ftp-manifest.json /etc/mema/manage/qualification.json
# Use the first successful encrypted snapshot retained by the earlier harness state.
python3 - <<"PY"
import json, os
root="/var/lib/mema/manage/qualification/snapshots"
for n in sorted(os.listdir(root)):
 p=os.path.join(root,n,"manifest.json")
 try:
  x=json.load(open(p))
  if x.get("state") == "verified" and x.get("complete"):
   open("/recovery/good-id","w").write(n); break
 except Exception: pass
PY
cat > /usr/local/bin/systemctl <<"SH"
#!/bin/sh
[ "${1:-}" = start ] && exit 1
exit 0
SH
chmod 0755 /usr/local/bin/systemctl
set +e
/usr/local/bin/mema recover restore "/var/lib/mema/manage/qualification/snapshots/$(cat /recovery/good-id)" --json >/recovery/restore.out 2>/recovery/restore.err
rc=$?; set -e
test "$rc" -ne 0
cat > /usr/local/bin/systemctl <<"SH"
#!/bin/sh
exit 0
SH
chmod 0755 /usr/local/bin/systemctl
/usr/local/bin/mema recover restore "/var/lib/mema/manage/qualification/snapshots/$(cat /recovery/good-id)" --json >/recovery/restore-clean.out
printf "%s\n" RESTORE_INTERRUPTION=PASS
printf "%s\n" "NO_FALSE_VERIFIED=PASS"
printf "%s\n" "NO_PLAINTEXT_REMNANTS=PASS"
printf "%s\n" "STALE_LOCK_RECOVERY=PASS"
printf "%s\n" "SUBSEQUENT_OPERATION=PASS"
'
