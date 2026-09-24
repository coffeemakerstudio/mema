#!/usr/bin/env bash
set -Eeuo pipefail
ROOT=$(realpath "$(dirname "$0")/..")
IMAGE="mema-safe-qualification-$$"
NAME="mema-safe-qualification-$$"
EXPORT_ARGS=()
if [ -n "${MEMA_QUAL_EXPORT_DIR:-}" ]; then
    mkdir -m 700 -p "$MEMA_QUAL_EXPORT_DIR"
    EXPORT_ARGS=(-v "$MEMA_QUAL_EXPORT_DIR:/export")
fi
cleanup(){ docker rm -f "$NAME" >/dev/null 2>&1 || true; docker image rm -f "$IMAGE" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker build --quiet -f "$ROOT/tests/manage_qualification/Dockerfile" -t "$IMAGE" "$ROOT" >/dev/null
docker run --rm --init --name "$NAME" --network=none --memory=1g --cpus=2 --pids-limit=256 "${EXPORT_ARGS[@]}" "$IMAGE" bash -Eeuo pipefail -c '
export MEMA_MANAGE_MANIFEST_DIR=/etc/mema/manage MEMA_MANAGE_STATE_DIR=/var/lib/mema/manage MEMA_MANAGE_GPG_HOME=/recovery-gpg
mkdir -m 700 -p /recovery-gpg /recovery
set -a; . /etc/mema-qualification/service.env; set +a
 gpg --batch --pinentry-mode loopback --passphrase "" --homedir /recovery-gpg \
   --quick-generate-key "qualification <qualification@example.invalid>" default default 1d >/dev/null 2>&1
/opt/mema-qualification/bin/service.py >/recovery/app.log 2>&1 & APP_PID=$!
trap "kill $APP_PID 2>/dev/null || true" EXIT
for i in $(seq 1 30); do curl -fsS http://127.0.0.1:18080/ready >/dev/null && break; sleep 0.2; done
curl -fsS http://127.0.0.1:18080/ready | grep -q true
curl -fsS http://127.0.0.1:18080/version | grep -q 1.0.0
curl -fsS http://127.0.0.1:18080/data | grep -q sqlite-record
sha256sum /var/lib/mema-qualification/app.db /var/lib/mema-qualification/data/persistent.txt /var/lib/mema-qualification/identity/marker > /recovery/baseline.sha256
/usr/local/bin/mema manage qualification verify --json > /recovery/verify.json
python3 -c "import json; x=json.load(open(\"/recovery/verify.json\")); assert x[\"status\"] == \"ok\", x"
/usr/local/bin/mema manage qualification snapshot --json > /recovery/snapshot.json
python3 - <<"PY"
import json, os, shutil
x=json.load(open("/recovery/snapshot.json"))
assert x["state"] == "verified" and x["complete"] is True, x
assert x["encryption"] == "gpg-public-key", x
assert x["ciphertext"] == "payload.gpg", x
snapshot=os.path.join("/var/lib/mema/manage/qualification/snapshots", x["id"])
assert os.path.isfile(os.path.join(snapshot,"payload.gpg"))
assert b"secret-fixture-not-for-output" not in open(os.path.join(snapshot,"payload.gpg"),"rb").read()
shutil.copytree(snapshot, "/recovery/snapshot")
if os.path.isdir("/export"):
    shutil.copytree("/recovery/snapshot", "/export/snapshot")
    shutil.copytree("/recovery-gpg", "/export/recovery-gpg", ignore=lambda _src, names: [name for name in names if not os.path.isfile(os.path.join(_src, name)) and not os.path.isdir(os.path.join(_src, name))])
    for root, dirs, files in os.walk("/recovery-gpg"):
        rel=os.path.relpath(root, "/recovery-gpg")
        target="/export/recovery-gpg" if rel == "." else os.path.join("/export/recovery-gpg", rel)
        os.makedirs(target, exist_ok=True)
        for name in files:
            source=os.path.join(root, name)
            if os.path.isfile(source): shutil.copy2(source, os.path.join(target, name))
    os.chmod("/export/snapshot", 0o755)
    os.chmod("/export/recovery-gpg", 0o700)
open("/recovery/snapshot-id","w").write(x["id"])
PY
python3 - <<'PY'
import json
assert any(json.loads(line)["status"] == "ok" for line in open("/var/lib/mema/manage/qualification/operations.jsonl"))
PY
kill "$APP_PID" 2>/dev/null || true; wait "$APP_PID" 2>/dev/null || true
rm -rf /opt/mema-qualification /var/lib/mema-qualification /etc/mema-qualification /etc/mema/manage
rm -f /etc/systemd/system/mema-qualification.service /etc/nginx/sites-available/mema-qualification /etc/nginx/sites-enabled/mema-qualification
rm -rf /var/lib/mema/manage
! test -e /opt/mema-qualification/bin/service.py
! test -e /var/lib/mema-qualification/app.db
! test -e /etc/systemd/system/mema-qualification.service
/usr/local/bin/mema recover inspect /recovery/snapshot --json > /recovery/inspect.json
/usr/local/bin/mema recover verify /recovery/snapshot --json > /recovery/recover-verify.json
/usr/local/bin/mema recover restore /recovery/snapshot --json > /recovery/restore.json
test -f /opt/mema-qualification/bin/service.py
test -f /var/lib/mema-qualification/app.db
test -f /etc/systemd/system/mema-qualification.service
test -f /etc/nginx/sites-available/mema-qualification
test -L /etc/nginx/sites-enabled/mema-qualification
test "$(readlink /var/lib/mema-qualification/current)" = /var/lib/mema-qualification/data
sha256sum -c /recovery/baseline.sha256
sqlite3 /var/lib/mema-qualification/app.db "pragma integrity_check" | grep -qx ok
test "$(stat -c %a /etc/mema-qualification/service.env)" = 600
test "$(stat -c %a /var/lib/mema-qualification/app.db)" = 600
test "$(cat /var/lib/mema-qualification/identity/marker)" = identity-marker
/opt/mema-qualification/bin/service.py & RESTORED_PID=$!
trap "kill $RESTORED_PID 2>/dev/null || true" EXIT
for i in $(seq 1 30); do curl -fsS http://127.0.0.1:18080/ready >/dev/null && break; sleep 0.2; done
curl -fsS http://127.0.0.1:18080/ready | grep -q true || { cat /recovery/app.log; exit 1; }
curl -fsS http://127.0.0.1:18080/version | grep -q 1.0.0
curl -fsS http://127.0.0.1:18080/data | grep -q sqlite-record
printf "snapshot id: %s\n" "$(cat /recovery/snapshot-id)"
printf "%s\n" "FILESYSTEM/APPLICATION RECOVERY IN DOCKER: PASS"
printf "%s\n" "systemd filesystem restoration: PASS"
printf "%s\n" "systemd runtime activation: NOT QUALIFIED IN DOCKER"
printf "%s\n" "nginx config recovery: PASS"
printf "%s\n" "real host nginx lifecycle: QUALIFY IN VM"
'
