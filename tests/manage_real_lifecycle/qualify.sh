#!/usr/bin/env bash
set -Eeuo pipefail
ROOT=$(realpath "$(dirname "$0")/../..")
MEMA="$ROOT/core/mema"
TOKEN="${BASHPID}-$(date +%s)"
BASE="$HOME/.local/share/mema-qualification"
QROOT="$BASE/run-$TOKEN"
MANIFEST_DIR="$QROOT/manifest"
STATE_DIR="$QROOT/mema-state"
REMOTE_DIR="$QROOT/encrypted-remote"
APP_DIR="$QROOT/app"
DATA_DIR="$QROOT/data"
NGINX_PREFIX="$QROOT/nginx"
NGINX_CONF="$NGINX_PREFIX/conf/nginx.conf"
NGINX_STATE="$QROOT/nginx-runtime"
CONTROL_DIR="$QROOT/control"
UNIT_DIR="$HOME/.config/systemd/user"
APP_UNIT=mema-qualification-app.service
NGINX_UNIT=mema-qualification-nginx.service
BACKEND_PORT=18181
NGINX_PORT=18180
RECIPIENT=qualification-$TOKEN@example.invalid
APP_STARTED=0
NGINX_STARTED=0
CREATED_UNITS=0

fail() { echo "qualification error: $*" >&2; exit 1; }
cleanup() {
    local status=$?
    set +e
    rm -f "$CONTROL_DIR/fail-stop" "$CONTROL_DIR/ignore-term" "$CONTROL_DIR/fail-start" "$CONTROL_DIR/fail-health"
    if ((NGINX_STARTED)); then systemctl --user stop "$NGINX_UNIT" >/dev/null 2>&1; fi
    if ((APP_STARTED)); then systemctl --user stop "$APP_UNIT" >/dev/null 2>&1; fi
    if ((CREATED_UNITS)); then
        rm -f "$UNIT_DIR/$APP_UNIT" "$UNIT_DIR/$NGINX_UNIT"
        systemctl --user daemon-reload >/dev/null 2>&1
    fi
    if [[ "${MEMA_QUAL_KEEP:-0}" != 1 ]]; then rm -rf -- "$QROOT"; fi
    if ((status != 0)); then echo "qualification artifacts: $QROOT" >&2; fi
    exit "$status"
}
trap cleanup EXIT

[[ -x "$MEMA" ]] || fail "build core/mema first"
[[ -x /usr/sbin/nginx ]] || fail "real nginx binary /usr/sbin/nginx is unavailable"
[[ -x /usr/bin/python3 ]] || fail "system Python 3 is unavailable"
USER_MANAGER_STATE=$(systemctl --user is-system-running 2>/dev/null || true)
[[ "$USER_MANAGER_STATE" == running || "$USER_MANAGER_STATE" == degraded ]] || fail "user systemd manager is not running"
for unit in "$APP_UNIT" "$NGINX_UNIT"; do
    [[ ! -e "$UNIT_DIR/$unit" ]] || fail "refusing to overwrite existing user unit $UNIT_DIR/$unit"
    if systemctl --user is-active --quiet "$unit"; then fail "refusing to touch already-active $unit"; fi
done
if ss -H -ltn "sport = :$BACKEND_PORT or sport = :$NGINX_PORT" | grep -q .; then
    fail "qualification ports $BACKEND_PORT/$NGINX_PORT are already in use"
fi

umask 077
mkdir -p "$MANIFEST_DIR" "$STATE_DIR" "$REMOTE_DIR" "$APP_DIR" "$DATA_DIR" \
    "$NGINX_PREFIX/conf" "$NGINX_STATE" "$CONTROL_DIR" "$UNIT_DIR"
cp "$ROOT/tests/manage_real_lifecycle/app.py" "$APP_DIR/service.py"
cp "$ROOT/tests/manage_real_lifecycle/stop-hook.py" "$APP_DIR/stop-hook.py"
chmod 0755 "$APP_DIR/service.py" "$APP_DIR/stop-hook.py"
python3 "$ROOT/tests/manage_real_lifecycle/mutate_state.py" "$DATA_DIR" A

cat >"$NGINX_CONF" <<EOF
worker_processes 1;
pid $NGINX_STATE/nginx.pid;
error_log $NGINX_STATE/error.log warn;
events { worker_connections 64; }
http {
  access_log $NGINX_STATE/access.log;
  server {
    listen 127.0.0.1:$NGINX_PORT;
    location = /nginx-health { default_type text/plain; return 200 "nginx-qualification-ok\\n"; }
    location / {
      proxy_set_header Host \$host;
      proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
      proxy_pass http://127.0.0.1:$BACKEND_PORT;
    }
  }
}
EOF

cat >"$UNIT_DIR/$APP_UNIT" <<EOF
[Unit]
Description=Mema isolated qualification application
After=default.target

[Service]
Type=simple
WorkingDirectory=$APP_DIR
Environment=MEMA_QUAL_DATA=$DATA_DIR
Environment=MEMA_QUAL_CONTROL=$CONTROL_DIR
Environment=MEMA_QUAL_BACKEND_PORT=$BACKEND_PORT
ExecStart=/usr/bin/python3 $APP_DIR/service.py
ExecStop=/usr/bin/python3 $APP_DIR/stop-hook.py
Restart=no
TimeoutStartSec=10
TimeoutStopSec=3
KillSignal=SIGTERM
KillMode=control-group

[Install]
WantedBy=default.target
EOF

cat >"$UNIT_DIR/$NGINX_UNIT" <<EOF
[Unit]
Description=Mema isolated qualification nginx reverse proxy
After=$APP_UNIT

[Service]
Type=simple
ExecStart=/usr/sbin/nginx -p $NGINX_PREFIX -c $NGINX_CONF -g 'daemon off;'
ExecReload=/usr/sbin/nginx -p $NGINX_PREFIX -c $NGINX_CONF -s reload
ExecStop=/usr/sbin/nginx -p $NGINX_PREFIX -c $NGINX_CONF -s quit
Restart=no
TimeoutStartSec=10
TimeoutStopSec=5
KillSignal=SIGQUIT
KillMode=control-group

[Install]
WantedBy=default.target
EOF
CREATED_UNITS=1
systemctl --user daemon-reload

# Use isolated keyrings: Mema's writer home contains only the recipient public key.
RECOVERY_GPG="$QROOT/recovery-gpg"
WRITER_GPG="$QROOT/writer-gpg-public-only"
mkdir -m 700 "$RECOVERY_GPG" "$WRITER_GPG"
gpg --batch --pinentry-mode loopback --passphrase '' --homedir "$RECOVERY_GPG" \
    --quick-generate-key "Mema qualification $TOKEN <$RECIPIENT>" default default 1d >/dev/null 2>&1
gpg --batch --homedir "$RECOVERY_GPG" --export "$RECIPIENT" >"$QROOT/recovery-public.gpg"
gpg --batch --homedir "$WRITER_GPG" --import "$QROOT/recovery-public.gpg" >/dev/null 2>&1
if gpg --batch --with-colons --homedir "$WRITER_GPG" --list-secret-keys | grep -Eq '^(sec|ssb):'; then
    fail "backup writer keyring unexpectedly has private key material"
fi

cat >"$QROOT/backends.json" <<EOF
{"qualification":{"name":"qualification","type":"local","root":"$REMOTE_DIR"}}
EOF
cat >"$MANIFEST_DIR/qualification.json" <<EOF
{
  "version": 1,
  "service": "qualification",
  "files": [
    {"path":"$APP_DIR/service.py","role":"application"},
    {"path":"$APP_DIR/stop-hook.py","role":"lifecycle-hook"},
    {"path":"$UNIT_DIR/$APP_UNIT","role":"systemd-unit"},
    {"path":"$UNIT_DIR/$NGINX_UNIT","role":"systemd-unit"}
  ],
  "config": [
    {"path":"$NGINX_CONF","role":"isolated-nginx-config"}
  ],
  "data": [
    {"path":"$DATA_DIR","role":"persistent-application-state","exclude":["runtime/service.pid","runtime/cache.tmp"]}
  ],
  "targets": {
    "app": {"type":"systemd","scope":"user","units":["$APP_UNIT"]},
    "nginx": {"type":"nginx","scope":"user","units":["$NGINX_UNIT"],"binary":"/usr/sbin/nginx","config":"$NGINX_CONF","prefix":"$NGINX_PREFIX"}
  },
  "health": {
    "ready":"http://127.0.0.1:$NGINX_PORT/ready",
    "version":"http://127.0.0.1:$NGINX_PORT/version"
  },
  "snapshot": {"consistency":"stop-service"},
  "variables": {
    "BACKEND": {"type":"backend-ref","default":"qualification"},
    "DR_RECIPIENT": {"type":"secret-ref","source":{"env":"MEMA_QUAL_RECIPIENT"}}
  },
  "backup": {
    "format":"mema-snapshot-v2",
    "backend":"\${BACKEND}",
    "file":"\${SERVICE}-\${SNAPSHOT_ID}.mema",
    "encryption":{"type":"gpg","recipient":"\${DR_RECIPIENT}"}
  }
}
EOF

export MEMA_MANAGE_MANIFEST_DIR="$MANIFEST_DIR"
export MEMA_MANAGE_STATE_DIR="$STATE_DIR"
export MEMA_MANAGE_BACKENDS_FILE="$QROOT/backends.json"
export MEMA_MANAGE_GPG_HOME="$WRITER_GPG"
export MEMA_QUAL_RECIPIENT="$RECIPIENT"

systemctl --user start "$APP_UNIT"
APP_STARTED=1
systemctl --user start "$NGINX_UNIT"
NGINX_STARTED=1
for _ in $(seq 1 50); do
    curl -fsS "http://127.0.0.1:$NGINX_PORT/ready" >/dev/null 2>&1 && break
    sleep 0.1
done
systemctl --user is-active --quiet "$APP_UNIT" || fail "application unit not active"
systemctl --user is-active --quiet "$NGINX_UNIT" || fail "nginx unit not active"
curl -fsS "http://127.0.0.1:$BACKEND_PORT/ready" | grep -q '"ready": true' || fail "backend health failed"
curl -fsS "http://127.0.0.1:$NGINX_PORT/nginx-health" | grep -q 'nginx-qualification-ok' || fail "nginx health failed"
curl -fsS "http://127.0.0.1:$NGINX_PORT/state" >"$QROOT/state-A.json"
python3 - "$QROOT/state-A.json" <<'PY'
import json,sys
s=json.load(open(sys.argv[1]))
assert s["revision"] == s["db_revision"] == "A" and s["release"] == "release-A", s
assert s["count"] == s["db_count"] == 41 and s["integrity"] == "ok", s
assert s["records"] == ["alpha","beta","gamma"], s
PY
python3 "$ROOT/tests/manage_real_lifecycle/tree_manifest.py" "$DATA_DIR" >"$QROOT/tree-A.json"

# Measure the stop-service window and count failed proxy probes without changing
# production listeners. This reports observed outage, it does not optimize it.
PROBE_FAILURES="$QROOT/proxy-probe-failures"
: >"$PROBE_FAILURES"
(
  while kill -0 "$BASHPID" 2>/dev/null; do
    if ! curl -fsS --max-time 0.25 "http://127.0.0.1:$NGINX_PORT/nginx-health" >/dev/null 2>&1; then echo failed >>"$PROBE_FAILURES"; fi
    sleep 0.05
  done
) & PROBE_PID=$!
BACKUP_START=$(date +%s%N)
"$MEMA" manage qualification backup --json >"$QROOT/backup.json"
BACKUP_END=$(date +%s%N)
kill "$PROBE_PID" 2>/dev/null || true
wait "$PROBE_PID" 2>/dev/null || true
BACKUP_MS=$(( (BACKUP_END - BACKUP_START) / 1000000 ))
PROBE_COUNT=$(wc -l <"$PROBE_FAILURES")
python3 - "$QROOT/backup.json" <<'PY'
import json,sys
x=json.load(open(sys.argv[1]))
assert x["snapshot_format"] == 2 and x["operation"] == "backup", x
assert x["state"] == "verified" and x["complete"] is True, x
assert x["encryption"] == "gpg-public-key" and x["payload_file"] == "payload.gpg", x
assert x["encryption_recipient"] == "<configured>", x
assert any(item["path"].endswith("runtime/service.pid") for item in x["excluded"]), x["excluded"]
assert any(item["path"].endswith("runtime/cache.tmp") for item in x["excluded"]), x["excluded"]
PY
SNAPSHOT_ID=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "$QROOT/backup.json")
SNAPSHOT_DIR="$STATE_DIR/qualification/snapshots/$SNAPSHOT_ID"
test -f "$SNAPSHOT_DIR/payload.gpg"
test -f "$SNAPSHOT_DIR/manifest.sha256"
! find "$REMOTE_DIR" -type f -name '*.partial-*' -print -quit | grep -q .
systemctl --user is-active --quiet "$APP_UNIT" || fail "app not active after backup"
systemctl --user is-active --quiet "$NGINX_UNIT" || fail "nginx not active after backup"
curl -fsS "http://127.0.0.1:$BACKEND_PORT/ready" | grep -q '"ready": true' || fail "backend unhealthy after backup"
curl -fsS "http://127.0.0.1:$NGINX_PORT/nginx-health" | grep -q 'nginx-qualification-ok' || fail "nginx unhealthy after backup"
curl -fsS "http://127.0.0.1:$NGINX_PORT/state" >"$QROOT/state-after-backup.json"
cmp "$QROOT/state-A.json" "$QROOT/state-after-backup.json" || fail "backup changed semantic application state"
"$MEMA" manage qualification verify "$SNAPSHOT_ID" --json >"$QROOT/verify.json"
"$MEMA" manage qualification backups --json >"$QROOT/backups.json"
python3 - "$QROOT/backups.json" "$SNAPSHOT_ID" <<'PY'
import json,sys
items=json.load(open(sys.argv[1]))
assert len(items)==1 and items[0]["id"]==sys.argv[2] and items[0]["state"]=="verified", items
PY
# Public-only writer is no longer needed; recovery actions use the isolated
# recipient private-key home.
export MEMA_MANAGE_GPG_HOME="$RECOVERY_GPG"

# A real process ignores SIGTERM. The user systemd manager enforces the unit's
# stop timeout and kills only this fixture process. Since systemd reports a
# timeout result, MEMA must abort capture, restart units, and publish nothing.
SNAPSHOT_COUNT_BEFORE=$(find "$STATE_DIR/qualification/snapshots" -mindepth 1 -maxdepth 1 -type d | wc -l)
REMOTE_FILE_COUNT_BEFORE=$(find "$REMOTE_DIR" -type f | wc -l)
touch "$CONTROL_DIR/ignore-term"
TIMEOUT_START=$(date +%s)
set +e
"$MEMA" manage qualification snapshot --json >"$QROOT/term-timeout-snapshot.out" 2>"$QROOT/term-timeout-snapshot.err"
TIMEOUT_RC=$?
set -e
TIMEOUT_END=$(date +%s)
rm -f "$CONTROL_DIR/ignore-term"
((TIMEOUT_END - TIMEOUT_START >= 2)) || fail "systemd stop timeout was not exercised"
((TIMEOUT_RC != 0)) || fail "MEMA accepted a systemd timeout as a clean quiesce"
systemctl --user is-active --quiet "$APP_UNIT" || fail "app not restarted after SIGTERM refusal"
systemctl --user is-active --quiet "$NGINX_UNIT" || fail "nginx not active after stop timeout"
curl -fsS "http://127.0.0.1:$NGINX_PORT/ready" | grep -q '"ready": true' || fail "health failed after stop timeout"
[[ $(find "$STATE_DIR/qualification/snapshots" -mindepth 1 -maxdepth 1 -type d | wc -l) == "$SNAPSHOT_COUNT_BEFORE" ]] || fail "timed-out snapshot was published"
[[ $(find "$REMOTE_DIR" -type f | wc -l) == "$REMOTE_FILE_COUNT_BEFORE" ]] || fail "timed-out backup left remote artifacts"
echo "systemd_sigterm_refusal_timeout_seconds=$((TIMEOUT_END - TIMEOUT_START))"
echo "systemd_timeout_quiesce_rejected=PASS"

# ExecStop exits nonzero in the real user unit. Even though systemd completes
# the stop job, its Result=exit-code must make MEMA abort before capture.
touch "$CONTROL_DIR/fail-stop"
set +e
"$MEMA" manage qualification snapshot --json >"$QROOT/execstop-failure.out" 2>"$QROOT/execstop-failure.err"
EXECSTOP_RC=$?
set -e
rm -f "$CONTROL_DIR/fail-stop"
((EXECSTOP_RC != 0)) || fail "MEMA accepted a failed ExecStop result"
systemctl --user is-active --quiet "$APP_UNIT" || fail "app not active after ExecStop failure path"
systemctl --user is-active --quiet "$NGINX_UNIT" || fail "nginx not active after ExecStop failure path"
curl -fsS "http://127.0.0.1:$NGINX_PORT/ready" | grep -q '"ready": true' || fail "health failed after ExecStop failure path"
[[ $(find "$STATE_DIR/qualification/snapshots" -mindepth 1 -maxdepth 1 -type d | wc -l) == "$SNAPSHOT_COUNT_BEFORE" ]] || fail "ExecStop-failed snapshot was published"
echo "systemd_execstop_failure_rejected=PASS"

# Destructive B mutation: remove A-only objects and add B-only objects. Restore
# replaces the whole declared data directory, so stale B members must vanish.
python3 "$ROOT/tests/manage_real_lifecycle/mutate_state.py" "$DATA_DIR" B
rm -rf "$DATA_DIR/releases/A" "$DATA_DIR/nested/empty.dat"
curl -fsS "http://127.0.0.1:$NGINX_PORT/state" >"$QROOT/state-B.json"
python3 - "$QROOT/state-B.json" <<'PY'
import json,sys
s=json.load(open(sys.argv[1])); assert s["revision"]==s["db_revision"]=="B" and s["release"]=="release-B",s
assert s["count"]==s["db_count"]==900 and s["integrity"]=="ok",s
PY
test -e "$DATA_DIR/stale-b-only.txt" && test -e "$DATA_DIR/b-only-directory/stale.txt"

"$MEMA" manage qualification restore "$SNAPSHOT_ID" --json >"$QROOT/restore-A.json"
systemctl --user is-active --quiet "$APP_UNIT" || fail "app not active after restore"
systemctl --user is-active --quiet "$NGINX_UNIT" || fail "nginx not active after restore"
curl -fsS "http://127.0.0.1:$BACKEND_PORT/ready" | grep -q '"ready": true' || fail "backend health failed after restore"
curl -fsS "http://127.0.0.1:$NGINX_PORT/nginx-health" | grep -q 'nginx-qualification-ok' || fail "nginx health failed after restore"
curl -fsS "http://127.0.0.1:$NGINX_PORT/state" >"$QROOT/state-restored-A.json"
cmp "$QROOT/state-A.json" "$QROOT/state-restored-A.json" || fail "semantic state A did not round-trip"
python3 "$ROOT/tests/manage_real_lifecycle/tree_manifest.py" "$DATA_DIR" >"$QROOT/tree-restored-A.json"
cmp "$QROOT/tree-A.json" "$QROOT/tree-restored-A.json" || fail "filesystem tree, modes, owners, bytes, or symlinks differ"
! test -e "$DATA_DIR/stale-b-only.txt" || fail "stale B-only file survived directory replacement"
! test -e "$DATA_DIR/b-only-directory" || fail "stale B-only directory survived directory replacement"
test -e "$DATA_DIR/runtime/service.pid" || fail "runtime PID file was not regenerated"
test -e "$DATA_DIR/runtime/cache.tmp" || fail "runtime cache was not regenerated"

# Declared runtime targets have desired post-restore state active. Prove this is
# intentional even when both units were stopped before recovery.
systemctl --user stop "$NGINX_UNIT" "$APP_UNIT"
systemctl --user is-active --quiet "$APP_UNIT" && fail "app stop precondition failed"
systemctl --user is-active --quiet "$NGINX_UNIT" && fail "nginx stop precondition failed"
"$MEMA" manage qualification restore "$SNAPSHOT_ID" --json >"$QROOT/restore-from-stopped.json"
systemctl --user is-active --quiet "$APP_UNIT" || fail "declared app target was not activated from stopped state"
systemctl --user is-active --quiet "$NGINX_UNIT" || fail "declared nginx target was not activated from stopped state"
curl -fsS "http://127.0.0.1:$NGINX_PORT/ready" | grep -q '"ready": true' || fail "health failed after stopped-service restore"

# A starts unsuccessfully only when the restored A revision is present. The
# real systemd start failure must trigger rollback to B and a healthy restart.
python3 "$ROOT/tests/manage_real_lifecycle/mutate_state.py" "$DATA_DIR" B
rm -rf "$DATA_DIR/releases/A" "$DATA_DIR/nested/empty.dat"
python3 "$ROOT/tests/manage_real_lifecycle/tree_manifest.py" "$DATA_DIR" >"$QROOT/tree-B-before-start-failure.json"
printf 'A\n' >"$CONTROL_DIR/fail-start"
set +e
"$MEMA" manage qualification restore "$SNAPSHOT_ID" --json >"$QROOT/start-failure.out" 2>"$QROOT/start-failure.err"
START_RC=$?
set -e
rm -f "$CONTROL_DIR/fail-start"
((START_RC != 0)) || fail "real systemd app start failure was reported as success"
systemctl --user is-active --quiet "$APP_UNIT" || fail "app not active after start-failure rollback"
systemctl --user is-active --quiet "$NGINX_UNIT" || fail "nginx not active after start-failure rollback"
curl -fsS "http://127.0.0.1:$NGINX_PORT/ready" | grep -q '"ready": true' || fail "health failed after start-failure rollback"
python3 "$ROOT/tests/manage_real_lifecycle/tree_manifest.py" "$DATA_DIR" >"$QROOT/tree-B-after-start-failure.json"
cmp "$QROOT/tree-B-before-start-failure.json" "$QROOT/tree-B-after-start-failure.json" || fail "systemd start failure did not roll back B"

# Make restored A unhealthy after promotion. The real unit starts; Mema's
# application health probe fails; Mema must restore B and reactivate both units.
python3 "$ROOT/tests/manage_real_lifecycle/mutate_state.py" "$DATA_DIR" B
rm -rf "$DATA_DIR/releases/A" "$DATA_DIR/nested/empty.dat"
curl -fsS "http://127.0.0.1:$NGINX_PORT/state" >"$QROOT/state-B-before-failure.json"
python3 "$ROOT/tests/manage_real_lifecycle/tree_manifest.py" "$DATA_DIR" >"$QROOT/tree-B-before-failure.json"
printf 'A\n' >"$CONTROL_DIR/fail-health"
set +e
"$MEMA" manage qualification restore "$SNAPSHOT_ID" --json >"$QROOT/health-failure.out" 2>"$QROOT/health-failure.err"
HEALTH_RC=$?
set -e
((HEALTH_RC != 0)) || fail "post-promotion application health failure was reported as success"
rm -f "$CONTROL_DIR/fail-health"
systemctl --user is-active --quiet "$APP_UNIT" || fail "app not active after health-failure rollback"
systemctl --user is-active --quiet "$NGINX_UNIT" || fail "nginx not active after health-failure rollback"
curl -fsS "http://127.0.0.1:$NGINX_PORT/ready" | grep -q '"ready": true' || fail "health did not recover to B after rollback"
curl -fsS "http://127.0.0.1:$NGINX_PORT/state" >"$QROOT/state-B-after-failure.json"
cmp "$QROOT/state-B-before-failure.json" "$QROOT/state-B-after-failure.json" || fail "pre-restore B state was not rolled back"
python3 "$ROOT/tests/manage_real_lifecycle/tree_manifest.py" "$DATA_DIR" >"$QROOT/tree-B-after-failure.json"
cmp "$QROOT/tree-B-before-failure.json" "$QROOT/tree-B-after-failure.json" || fail "pre-restore B filesystem was not rolled back"
test -f "$SNAPSHOT_DIR/payload.gpg" || fail "recovery payload was lost after rollback"
python3 - "$STATE_DIR/qualification/operations.jsonl" <<'PY'
import json,sys
ops=[json.loads(x) for x in open(sys.argv[1])]
assert any(x.get("type")=="restore" and x.get("status")=="failed" and "post-restore health" in x.get("error","") for x in ops),ops
PY

# Remove the marker and demonstrate a clean retry from B to A.
"$MEMA" manage qualification restore "$SNAPSHOT_ID" --json >"$QROOT/restore-retry.json"
curl -fsS "http://127.0.0.1:$NGINX_PORT/state" >"$QROOT/state-final-A.json"
cmp "$QROOT/state-A.json" "$QROOT/state-final-A.json" || fail "normal retry did not recover state A"

# Exercise nginx/backend disagreement without touching any system nginx service.
systemctl --user stop "$NGINX_UNIT"
curl -fsS "http://127.0.0.1:$BACKEND_PORT/ready" | grep -q '"ready": true' || fail "backend failed while nginx stopped"
if curl -fsS --max-time 1 "http://127.0.0.1:$NGINX_PORT/ready" >/dev/null 2>&1; then fail "nginx endpoint unexpectedly worked while its isolated unit was stopped"; fi
systemctl --user start "$NGINX_UNIT"
for _ in $(seq 1 30); do curl -fsS "http://127.0.0.1:$NGINX_PORT/ready" >/dev/null 2>&1 && break; sleep 0.1; done
curl -fsS "http://127.0.0.1:$NGINX_PORT/ready" | grep -q '"ready": true' || fail "nginx/backend did not recover after restart"

# Clean same-host recovery: stop the isolated units, remove every managed
# application/config/data/unit resource, and reload the user manager so it no
# longer has the unit definitions cached. The only recovery inputs are the
# encrypted v2 snapshot and its dedicated recipient private key.
CLEAN_BUNDLE="$QROOT/clean-recovery-bundle"
cp -a "$SNAPSHOT_DIR" "$CLEAN_BUNDLE"
systemctl --user stop "$NGINX_UNIT" "$APP_UNIT"
rm -rf -- "$APP_DIR" "$DATA_DIR" "$NGINX_PREFIX" "$NGINX_STATE"
mkdir -m 700 -p "$NGINX_STATE"
rm -f -- "$UNIT_DIR/$APP_UNIT" "$UNIT_DIR/$NGINX_UNIT"
systemctl --user daemon-reload
! test -e "$APP_DIR/service.py" || fail "clean recovery retained application code"
! test -e "$DATA_DIR/application.json" || fail "clean recovery retained application state"
! test -e "$NGINX_CONF" || fail "clean recovery retained nginx configuration"
"$MEMA" recover restore "$CLEAN_BUNDLE" --json >"$QROOT/clean-recovery.json"
systemctl --user is-active --quiet "$APP_UNIT" || fail "clean recovery did not start app unit"
systemctl --user is-active --quiet "$NGINX_UNIT" || fail "clean recovery did not start nginx unit"
curl -fsS "http://127.0.0.1:$BACKEND_PORT/ready" | grep -q '"ready": true' || fail "clean recovery backend health failed"
curl -fsS "http://127.0.0.1:$NGINX_PORT/nginx-health" | grep -q 'nginx-qualification-ok' || fail "clean recovery nginx health failed"
curl -fsS "http://127.0.0.1:$NGINX_PORT/state" >"$QROOT/clean-recovered-state.json"
cmp "$QROOT/state-A.json" "$QROOT/clean-recovered-state.json" || fail "clean recovery semantic state differs"
python3 "$ROOT/tests/manage_real_lifecycle/tree_manifest.py" "$DATA_DIR" >"$QROOT/clean-recovered-tree.json"
cmp "$QROOT/tree-A.json" "$QROOT/clean-recovered-tree.json" || fail "clean recovery filesystem metadata differs"

# A real app process can ignore SIGTERM; systemd's TimeoutStopSec bounds the
# stop, kills it, and Mema restarts the declared unit after capture.
# (A 3-second timeout is set by this isolated unit.)

echo "REAL_SYSTEMD_NGINX_QUALIFICATION=PASS"
echo "snapshot_id=$SNAPSHOT_ID"
echo "backup_duration_ms=$BACKUP_MS"
echo "nginx_probe_failures_during_stop-service_backup=$PROBE_COUNT"
echo "same_host_destructive_restore=PASS"
echo "stopped_before_restore=activated_as_declared=PASS"
echo "post_promotion_systemd_start_failure_rollback=PASS"
echo "post_promotion_health_failure_rollback=PASS"
echo "nginx_backend_disagreement_detection=PASS"
echo "clean_same_host_user_systemd_recovery=PASS"
echo "qualification_root=$QROOT"
