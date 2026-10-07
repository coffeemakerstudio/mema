# Mema Manage

`mema manage` is the canonical public interface for applications that opt in with
an installed **Mema Manage Manifest v1**. `mmanage` is not a Mema interface.

## Manifest discovery

A trusted package installs `/etc/mema/manage/<service>.json`. Local manifests
may be placed in `$HOME/.local/share/mema/manage/<service>.json`. Tests and
isolated installations can set `MEMA_MANAGE_MANIFEST_DIR` and
`MEMA_MANAGE_STATE_DIR`.

The manifest is JSON (and is intentionally self-describing so a recovery tool
does not need the original application). Its top-level shape is:

```json
{
  "version": 1,
  "service": "example",
  "files": [{"path": "/etc/example/unit.service", "role": "systemd-unit"}],
  "config": [{"path": "/etc/nginx/sites-available/example", "role": "nginx-config"}],
  "env": [{"path": "/etc/example/env", "secret": true}],
  "data": [{"path": "/var/lib/example"}],
  "identity": [{"path": "/var/lib/example/keys", "secret": true, "critical": true}],
  "binary": [{"path": "/opt/example/bin/example"}],
  "cleanup": [{"path": "/var/lib/example/old", "clean": "quarantine"}],
  "targets": {
    "runtime": {"type": "systemd", "scope": "system", "units": ["example.service"]},
    "reverse_proxy": {"type": "nginx", "configs": ["/etc/nginx/sites-available/example"]}
  },
  "health": {"ready": "http://127.0.0.1:8080/ready", "version": "http://127.0.0.1:8080/version"},
  "snapshot": {"consistency": "live"},
  "clean": {"grace_period": "7d"}
}
```

Paths and targets come only from the trusted manifest; callers cannot provide
filesystem paths, systemd units, or shell commands. Resource categories are
semantic. Identity resources are protected by default. `cleanup` is the only
category eligible for quarantine, and only when its policy is not `never`.

## Commands

```text
mema manage <service> status [--json]
mema manage <service> verify [--json]
mema manage <service> snapshot [--dry-run] [--print] [--json]
mema manage <service> snapshots [--json]
mema manage <service> backup [--dry-run] [--json]
mema manage <service> backups [--json]
mema manage <service> restore <snapshot> [--dry-run] [--print] [--json]
mema manage <service> update [<version>] [--json]
mema manage <service> rollback [<version-or-snapshot>] [--json]
mema manage <service> clean [--dry-run] [--print] [--json]
mema manage <service> clean full [--dry-run] [--print] [--json]
mema manage <service> clean status [--json]
mema manage <service> clean undo <operation-id> [--json]
mema manage <service> logs [--json]
mema manage <service> config [--json]
```

Snapshots preserve original absolute destinations in metadata and capture file,
directory, symlink, mode, ownership metadata, and SHA-256 values for regular
files. Restore stages and verifies replacements, promotes them transactionally,
and rolls back on activation or health failure. Declared systemd units preserve
their running state during `snapshot` quiescence; restore treats declared units
as desired-active targets and starts them even if they were stopped before the
restore. Systemd targets accept `scope: "system"` (the default) or `scope:
"user"` and never fall back between managers.

A dedicated nginx instance can be declared with a trusted executable, its main
configuration, prefix, and the exact systemd unit that owns it:

```json
"reverse_proxy": {
  "type": "nginx", "scope": "user",
  "units": ["example-nginx.service"],
  "binary": "/usr/sbin/nginx",
  "config": "/home/example/.local/share/example-nginx/nginx.conf",
  "prefix": "/home/example/.local/share/example-nginx/"
}
```

For this isolated form, MEMA tests only that nginx configuration (`-t -p … -c
…`) and starts/reloads/stops only the declared unit. It does not reload the host's
global nginx service. Legacy nginx targets without an explicit unit retain the
system nginx reload behavior.

`clean` renames eligible resources into managed quarantine. `clean full` first
performs normal quarantine and then considers only **previously** quarantined,
expired entries. A newly quarantined entry can never be purged in the same
invocation. Permanent purge requires a complete snapshot covering the original
path and matching digest where available. `clean undo` refuses to overwrite a
new object at the original destination.

Each mutating operation takes a per-service lock and appends a JSON operation
record under the management state directory. Incomplete operations remain
visible in the journal. `--json` is available for automation and future adapters.

Mutating operations support `--dry-run` and `--print`. A dry run resolves the
same layered configuration as the real operation, reports manifest-owned
resources, runtime target facts, backup settings, conflicts, and the ordered
plan, but performs no service, filesystem, nginx, backend, quarantine, or
operation-state mutation. JSON plans are suitable for SSH-based automation:

```sh
ssh production-host 'mema manage tparun snapshot --dry-run --print --json'
```

Resource paths and target names remain trusted manifest data. Secret values are
never printed; declared secret locations and safe metadata such as ownership,
permissions, and binary hashes may be shown to an authorized local operator.
SSH is the management trust boundary; Mema does not add a private HTTP
management endpoint or a second management API key.

## Typed configuration and backup policy

The manifest may declare typed variables. Only declared variables marked
`overridable: true` accept invocation overrides:

```json
{
  "variables": {
    "BACKUP_BACKEND": {"type":"backend-ref", "default":"local", "overridable":true},
    "BACKUP_FILE": {"type":"string", "default":"${SERVICE}-${SNAPSHOT_ID}.mema", "overridable":true},
    "CLEAN_GRACE": {"type":"duration", "default":"7d", "overridable":true},
    "BACKUP_RECIPIENT": {"type":"secret-ref", "source":{"env":"MEMA_BACKUP_RECIPIENT"}}
  },
  "backup": {
    "format":"mema-snapshot-v1",
    "backend":"${BACKUP_BACKEND}",
    "file":"${BACKUP_FILE}",
    "encryption":{"type":"gpg", "recipient":"${BACKUP_RECIPIENT}"},
    "verify":{"remote_hash":true}
  }
}
```

Configuration precedence is deterministic:

```text
manifest defaults
→ service-local <service>.local.json
→ host host.json
→ explicitly declared environment source
→ invocation --set NAME=VALUE
```

Use `mema manage SERVICE config --json` to inspect effective values and
provenance. Secret values are shown only as `<configured>` or `<missing>`.
Invocation overrides are ephemeral:

```text
mema manage SERVICE snapshot --set BACKUP_BACKEND=local --set BACKUP_FILE=manual.mema
```

Variable expansion is limited to declared backup fields and clean grace
configuration. Resource paths, systemd units, nginx targets, identity policy,
and other structural manifest fields cannot be overridden or interpolated.
Unknown variables, unsupported types, cycles, invalid durations, unknown
backends, and unsafe object names fail before a mutating operation begins.

## Encrypted and remote snapshots

New snapshots and backups use **Mema Snapshot Format v2**, independently of
Manifest v1. V1 snapshots remain readable for compatibility; new writes require
v2. Metadata records a random identity, embedded manifest checksum, payload
checksum, resource metadata, exclusions, backend, and lifecycle state. V2
publication is staged and a snapshot is not listed as verified unless its local
artifact and configured remote copy verify. `backup` and `backups` provide
explicit backup operations/listing; unknown formats and non-verified snapshots
fail closed during restore. The snapshot `mema_version` field identifies the
Manage engine version; it is informational and is distinct from the Debian
package version and `snapshot_format`, which governs snapshot-reader
compatibility. Managed restore selects the payload reader from snapshot
metadata and accepts v1-to-v2 manifest changes limited to backup
format/encryption policy and resource exclusion patterns. Captured resource
roots, targets, health checks, and other service configuration must still
match. Exclusions govern future capture; the artifact metadata defines the
restore contents. Prefix an exclusion with `./` to anchor it to the resource
root; an unanchored basename pattern matches at any depth. Standalone
`mema recover` uses the embedded manifest for restoration.

A manifest can select an encryption recipient and backend:

```json
{
  "snapshot": {
    "consistency": "stop-service",
    "backend": "backup-store",
    "recipient": "backup@example.invalid"
  }
}
```

Encryption uses established GnuPG public-key encryption; Mema does not
implement cryptographic primitives. For new remote v2 backups, Mema encrypts
both the payload and remote manifest before upload. The manifest metadata
records `metadata_encryption: "gpg-public-key"`; the remote manifest object is
`<payload-object>.manifest.gpg`, and plaintext manifest sidecars are not
published. Local Mema state retains its manifest and checksum sidecar. Older
remote snapshots without this field retain the legacy plaintext metadata
layout for verification compatibility.

The production host needs only the public recipient capability. Decryption uses
a trusted local GnuPG home configured by `MEMA_MANAGE_GPG_HOME`; the recovery
private key is not stored in manifests, snapshot metadata, logs, or the remote
store. OpenPGP integrity protection detects ciphertext modification but does
not authenticate the writer; writer provenance/signing is not provided.

Backend definitions are kept outside service manifests in
`/etc/mema/backends.json` or the file named by `MEMA_MANAGE_BACKENDS_FILE`:

```json
{
  "backup-store": {
    "type": "local",
    "root": "/srv/encrypted-mema-store"
  },
  "offsite": {
    "type": "ftp",
    "url": "ftp://backup.example.invalid/mema",
    "username": "mema-backup",
    "password_file": "/etc/mema/ftp-password",
    "tls": false
  }
}
```

The initial backends are `local` and FTP. FTP uploads reject plaintext and
OpenPGP ciphertext without MDC/AEAD integrity protection, so payload and
metadata cross the upload boundary as encrypted, integrity-protected
ciphertext. Plain FTP transport is not confidential; it is an accepted hosting
provider constraint for this deployment, not a confidentiality guarantee.
Transport authentication is distinct from snapshot encryption. Passwords are
read from a protected file and are not placed in command-line arguments or
logs. Remote verification downloads the stored ciphertext and compares its
SHA-256; an upload-success response alone never makes a snapshot recoverable.
FTP destination subpaths apply to transfers and to rename/delete control
commands.

For recovery from a remote backup, retrieve the payload and `.manifest.gpg`
sidecar, decrypt the sidecar with the recovery GnuPG home, and recreate
`manifest.sha256` from the decrypted `manifest.json`. The resulting standalone
recovery directory can be inspected, verified, and restored with `mema recover`.

The standalone recovery path is:

```text
mema recover inspect <snapshot-directory>
mema recover verify <snapshot-directory>
mema recover restore <snapshot-directory>
```

A snapshot contains an embedded service manifest and preserves absolute
filesystem destinations in metadata. Recovery decrypts and verifies the
payload before changing target files, so it does not depend on the original
Mema installation or package registry. The recovery private key is supplied
through the configured GnuPG home, not a command-line argument.

`clean full` still requires an existing, complete, matching snapshot before
purging protected quarantined content. A verified remote snapshot is required
when a remote backend is selected. Remote retention and deletion are not
performed automatically.

## Current boundaries

The foundation provides versioned encrypted snapshot metadata, GnuPG
public-key encryption, local and FTP backend adapters, ciphertext verification,
standalone inspect/verify/restore, manifest embedding, quarantine safety,
journal, locking, dry-run planning, and JSON output. Generic update/rollback
providers and MCP are intentionally not implemented. TPA.run private runtime
inspection belongs behind authorized SSH and Mema Manage; public `/ready` and
`/version` endpoints are not expanded with host topology or secrets.
