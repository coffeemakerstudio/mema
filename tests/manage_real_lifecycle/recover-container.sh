#!/usr/bin/env bash
set -Eeuo pipefail
ROOT=$(realpath "$(dirname "$0")/../..")
SNAPSHOT_DIR=${1:?usage: recover-container.sh <snapshot-directory> <recovery-gpg-home>}
RECOVERY_GPG=${2:?usage: recover-container.sh <snapshot-directory> <recovery-gpg-home>}
[[ -d "$SNAPSHOT_DIR" && -d "$RECOVERY_GPG" ]] || { echo "snapshot/key input is missing" >&2; exit 2; }
IMAGE="mema-real-recovery-$$"
NAME="mema-real-recovery-$$"
LOG="$SNAPSHOT_DIR/container-systemd-recovery.log"
cleanup() {
    local status=$?
    set +e
    docker logs "$NAME" >"$LOG" 2>&1
    docker rm -f "$NAME" >/dev/null 2>&1
    docker image rm -f "$IMAGE" >/dev/null 2>&1
    if ((status != 0)); then echo "container recovery failed; logs: $LOG" >&2; fi
    exit "$status"
}
trap cleanup EXIT

# The image contains only legitimate runtime prerequisites. At runtime the
# container has no network/host ports and receives only read-only backup/key
# mounts. Rootful Docker is used solely because PID-1 systemd needs a private
# cgroup namespace and SYS_ADMIN within the container; no host service/paths
# are mounted writable.
docker build --quiet -f "$ROOT/tests/manage_real_lifecycle/recovery-container.Dockerfile" -t "$IMAGE" "$ROOT" >/dev/null

docker run -d --name "$NAME" --network=none --memory=1g --cpus=2 --pids-limit=256 \
    --cgroupns=private --cap-add=SYS_ADMIN --security-opt seccomp=unconfined \
    --tmpfs /run:rw,nosuid,nodev,mode=755 --tmpfs /run/lock:rw,nosuid,nodev,mode=755 \
    --tmpfs /tmp:rw,nosuid,nodev,mode=1777 \
    --mount "type=bind,src=$SNAPSHOT_DIR,dst=/recovery-input,readonly" \
    --mount "type=bind,src=$RECOVERY_GPG,dst=/recovery-key-input,readonly" \
    "$IMAGE" >/dev/null

for _ in $(seq 1 60); do
    state=$(docker exec "$NAME" systemctl is-system-running 2>/dev/null || true)
    [[ "$state" == running || "$state" == degraded ]] && break
    sleep 0.25
done
[[ "${state:-}" == running || "${state:-}" == degraded ]] || { docker logs "$NAME" >&2; exit 1; }

# Derive the declared absolute destination root from the public snapshot
# resource catalog. No source application tree is copied into the container.
QUAL_ROOT=$(python3 - "$SNAPSHOT_DIR/manifest.json" <<'PY'
import json, pathlib, sys
meta=json.load(open(sys.argv[1]))
paths=[pathlib.Path(x["path"]) for x in meta["resources"]]
app=next((p for p in paths if p.as_posix().endswith("/app/service.py")), None)
if app is None: raise SystemExit("backup does not declare the qualification app")
print(app.parent.parent)
PY
)

docker exec "$NAME" bash -Eeuo pipefail -c '
useradd --create-home --uid 1000 --shell /bin/bash eugen 2>/dev/null || true
systemctl start systemd-logind.service 2>/dev/null || true
systemctl start user-runtime-dir@1000.service
systemctl start user@1000.service
loginctl enable-linger eugen
for i in $(seq 1 60); do
  if runuser -u eugen -- env XDG_RUNTIME_DIR=/run/user/1000 DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus systemctl --user show-environment >/dev/null 2>&1; then break; fi
  sleep .25
done
runuser -u eugen -- mkdir -p /home/eugen/.local/share/mema-qualification
cp -a /recovery-key-input /home/eugen/recovery-gpg
chown -R eugen:eugen /home/eugen/recovery-gpg
chmod 700 /home/eugen/recovery-gpg
' || { docker logs "$NAME" >&2; exit 1; }

# Provision only empty runtime directories required by the embedded isolated
# nginx config. These contain no source service/config/data state.
docker exec -u 0 "$NAME" mkdir -m 700 -p "$QUAL_ROOT/nginx/runtime"
docker exec -u 0 "$NAME" chown -R 1000:1000 "$QUAL_ROOT"

docker exec "$NAME" runuser -u eugen -- env \
    XDG_RUNTIME_DIR=/run/user/1000 \
    DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus \
    MEMA_MANAGE_GPG_HOME=/home/eugen/recovery-gpg \
    /usr/local/bin/mema recover verify /recovery-input --json

docker exec "$NAME" runuser -u eugen -- env \
    XDG_RUNTIME_DIR=/run/user/1000 \
    DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus \
    MEMA_MANAGE_GPG_HOME=/home/eugen/recovery-gpg \
    /usr/local/bin/mema recover restore /recovery-input --json

APP="$QUAL_ROOT/app"
DATA="$QUAL_ROOT/data"
NGINX_PORT=18180
BACKEND_PORT=18181

docker exec "$NAME" bash -Eeuo pipefail -c "
export XDG_RUNTIME_DIR=/run/user/1000 DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus
runuser -u eugen -- env XDG_RUNTIME_DIR=\$XDG_RUNTIME_DIR DBUS_SESSION_BUS_ADDRESS=\$DBUS_SESSION_BUS_ADDRESS systemctl --user is-active --quiet mema-qualification-app.service
runuser -u eugen -- env XDG_RUNTIME_DIR=\$XDG_RUNTIME_DIR DBUS_SESSION_BUS_ADDRESS=\$DBUS_SESSION_BUS_ADDRESS systemctl --user is-active --quiet mema-qualification-nginx.service
curl -fsS http://127.0.0.1:$BACKEND_PORT/ready | grep -q 'ready.*true'
curl -fsS http://127.0.0.1:$NGINX_PORT/nginx-health | grep -q nginx-qualification-ok
curl -fsS http://127.0.0.1:$NGINX_PORT/state >/tmp/recovered-state.json
python3 -c 'import json; x=json.load(open("/tmp/recovered-state.json")); assert x["revision"]==x["db_revision"]=="A" and x["release"]=="release-A" and x["count"]==x["db_count"]==41 and x["records"]==["alpha","beta","gamma"] and x["integrity"]=="ok", x'
test -L '$DATA/current'
test \"\$(readlink '$DATA/current')\" = releases/A
test \"\$(stat -c %a '$DATA/bin/run')\" = 751
test \"\$(stat -c %a '$DATA/nested/empty.dat')\" = 600
test ! -e '$DATA/stale-b-only.txt'
test ! -e '$DATA/b-only-directory'
test -f '$DATA/runtime/service.pid'
test -f '$DATA/runtime/cache.tmp'
test \"\$(cat '$DATA/runtime/service.pid')\" = \"\$(runuser -u eugen -- env XDG_RUNTIME_DIR=\$XDG_RUNTIME_DIR DBUS_SESSION_BUS_ADDRESS=\$DBUS_SESSION_BUS_ADDRESS systemctl --user show --property=MainPID --value mema-qualification-app.service)\"
printf '%s\\n' SYSTEMD_CONTAINER_ENCRYPTED_RECOVERY=PASS
"

# Export only test result/log output (not the recovered filesystem) next to the
# input bundle. Docker logs are saved by the EXIT handler after container exit.
echo "CONTAINER_RECOVERY=PASS"
echo "container_name=$NAME"
echo "container_network=none"
echo "container_mounts=backup-and-key-read-only"
echo "qualification_root=$QUAL_ROOT"
