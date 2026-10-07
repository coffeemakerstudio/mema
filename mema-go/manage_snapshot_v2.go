package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Tests install a deterministic fault at lifecycle boundaries; production has
// no environment-variable or CLI switch capable of activating these faults.
var manageFaultInjector func(string) error

func manageInjectFault(point string) error {
	if manageFaultInjector != nil {
		return manageFaultInjector(point)
	}
	return nil
}

func manageSnapshotID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return "snap-" + hex.EncodeToString(id[:]), nil
}

func manageSnapshotWithOverrides(m manageManifest, manifestPath, service string, s scope, overrides map[string]string) error {
	return manageSnapshotV2(m, manifestPath, service, s, overrides, "snapshot")
}

func manageBackupWithOverrides(m manageManifest, manifestPath, service string, s scope, overrides map[string]string) error {
	return manageSnapshotV2(m, manifestPath, service, s, overrides, "backup")
}

func manageSnapshotV2(m manageManifest, manifestPath, service string, s scope, overrides map[string]string, operation string) (result error) {
	builtins := map[string]string{"SERVICE": service, "SNAPSHOT_ID": "snapshot-pending", "TIMESTAMP": time.Now().UTC().Format(time.RFC3339Nano), "HOST": hostnameOrUnknown()}
	if _, err := resolveManageConfig(m, manifestPath, service, s, overrides, builtins); err != nil {
		return err
	}
	unlock, err := manageLock(service, s)
	if err != nil {
		return err
	}
	defer unlock()
	id, err := manageSnapshotID()
	if err != nil {
		return err
	}
	builtins["SNAPSHOT_ID"] = id
	builtins["TIMESTAMP"] = time.Now().UTC().Format(time.RFC3339Nano)
	cfg, err := resolveManageConfig(m, manifestPath, service, s, overrides, builtins)
	if err != nil {
		return err
	}
	if cfg.Backup.Format != "mema-snapshot-v2" {
		return fmt.Errorf("new snapshots require backup format mema-snapshot-v2, got %q", cfg.Backup.Format)
	}
	if err := validateSnapshotResourceSet(manageResources(m)); err != nil {
		return err
	}
	op := manageOperation{ID: manageID("op"), Service: service, Type: operation, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: "running"}
	manageRecord(op, s)
	snapshotsDir := filepath.Join(manageStateDir(s), service, "snapshots")
	if err := os.MkdirAll(snapshotsDir, 0o700); err != nil {
		return manageFail(op, err, s)
	}
	finalDir := filepath.Join(snapshotsDir, id)
	stagingDir, err := os.MkdirTemp(snapshotsDir, ".partial-"+id+"-")
	if err != nil {
		return manageFail(op, err, s)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stagingDir)
		}
	}()
	resume, err := managePrepareSnapshotV2(m)
	if err != nil {
		return manageFail(op, err, s)
	}
	resumed := resume == nil
	if resume != nil {
		defer func() {
			if !resumed {
				if resumeErr := resume(); resumeErr != nil {
					result = errors.Join(result, fmt.Errorf("resume service after snapshot: %w", resumeErr))
				}
			}
		}()
	}
	meta := manageSnapshotMeta{
		SnapshotFormat: manageSnapshotFormatVersion, ID: id, ManifestVersion: m.Version,
		MemaVersion: manageEngineVersion, Service: service, Operation: operation, CreatedAt: builtins["TIMESTAMP"],
		Consistency: m.Snapshot.Consistency, State: "creating", Complete: false,
		ServiceManifest: "service-manifest.json", Resources: []manageCaptured{}, Excluded: []manageExcluded{},
	}
	if meta.Consistency == "" {
		meta.Consistency = "live"
	}
	meta.Host = hostnameOrUnknown() // informational; never used to select restore paths.
	if err := os.MkdirAll(filepath.Join(stagingDir, "filesystem"), 0o700); err != nil {
		return manageFail(op, err, s)
	}
	if err := manageInjectFault("snapshot-create"); err != nil {
		return manageFail(op, err, s)
	}
	for index, resource := range manageResources(m) {
		dstRel := fmt.Sprintf("resources/%04d", index)
		dst := filepath.Join(stagingDir, "filesystem", filepath.FromSlash(dstRel))
		captured, excluded, err := manageCaptureV2(resource.Path, dst, dstRel, resource.Exclude, resource.ExcludeSockets)
		if err != nil {
			return manageFail(op, fmt.Errorf("capture %s: %w", resource.Path, err), s)
		}
		captured.SnapshotPath = dstRel
		meta.Resources = append(meta.Resources, captured)
		meta.Excluded = append(meta.Excluded, excluded...)
	}
	manifestBytes, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return manageFail(op, fmt.Errorf("serialize service manifest: %w", err), s)
	}
	if err := atomicWriteFile(filepath.Join(stagingDir, meta.ServiceManifest), manifestBytes, 0o600); err != nil {
		return manageFail(op, fmt.Errorf("embed service manifest: %w", err), s)
	}
	meta.ServiceManifestSHA256 = snapshotSHA256(manifestBytes)
	if err := validateSnapshotResources(meta, m); err != nil {
		return manageFail(op, fmt.Errorf("validate captured resource manifest: %w", err), s)
	}
	if err := verifySnapshotPayloadV2(meta, filepath.Join(stagingDir, "filesystem"), true); err != nil {
		return manageFail(op, fmt.Errorf("verify captured filesystem: %w", err), s)
	}
	plain := filepath.Join(stagingDir, "payload.tar.gz")
	if err := createSnapshotArchiveV2(stagingDir, plain); err != nil {
		return manageFail(op, err, s)
	}
	if err := manageInjectFault("snapshot-archive-created"); err != nil {
		return manageFail(op, err, s)
	}
	if resume != nil {
		if err := resume(); err != nil {
			return manageFail(op, fmt.Errorf("resume service after consistent capture: %w", err), s)
		}
		resumed = true
	}
	meta.Encryption = "none"
	meta.PayloadFile = "payload.tar.gz"
	if recipient := cfg.Backup.Encryption.Recipient; recipient != "" {
		meta.Encryption = "gpg-public-key"
		if cfg.BackupRecipientSecret {
			meta.EncryptionRecipient = "<configured>"
		} else {
			meta.EncryptionRecipient = recipient
		}
		cipher := filepath.Join(stagingDir, "payload.gpg")
		if err := encryptSnapshot(plain, cipher, recipient); err != nil {
			return manageFail(op, err, s)
		}
		if err := os.Remove(plain); err != nil {
			return manageFail(op, err, s)
		}
		meta.PayloadFile = "payload.gpg"
		meta.Ciphertext = meta.PayloadFile
	}
	if err := manageInjectFault("snapshot-encrypted"); err != nil {
		return manageFail(op, err, s)
	}
	if err := os.RemoveAll(filepath.Join(stagingDir, "filesystem")); err != nil {
		return manageFail(op, err, s)
	}
	if err := os.Remove(filepath.Join(stagingDir, meta.ServiceManifest)); err != nil {
		return manageFail(op, err, s)
	}
	payloadPath := filepath.Join(stagingDir, meta.PayloadFile)
	payloadInfo, err := os.Stat(payloadPath)
	if err != nil {
		return manageFail(op, err, s)
	}
	meta.Size = payloadInfo.Size()
	meta.PayloadSHA256 = manageHash(payloadPath)
	meta.CiphertextSHA256 = meta.PayloadSHA256
	if meta.PayloadSHA256 == "" {
		return manageFail(op, errors.New("payload checksum could not be computed"), s)
	}
	meta.Backend = cfg.Backup.Backend
	if meta.Backend == "" {
		meta.Backend = "local"
	}
	meta.BackupFormat = cfg.Backup.Format
	meta.BackupFile = cfg.Backup.File
	meta.Compression = cfg.Backup.Compression.Type
	if meta.Compression == "" {
		meta.Compression = "gzip"
	}
	backend, err := manageBackend(meta.Backend)
	if err != nil {
		return manageFail(op, err, s)
	}
	meta.BackendType = backend.Type
	remote := backend.Name != "local" || backend.Type != "local"
	if remote && meta.Encryption != "gpg-public-key" {
		return manageFail(op, errors.New("remote backup requires public-key encryption"), s)
	}
	if remote {
		object := filepath.ToSlash(filepath.Join(service, cfg.Backup.File))
		if err := validateBackendObject(object); err != nil {
			return manageFail(op, err, s)
		}
		if err := backendPublish(backend, object, payloadPath, meta.PayloadSHA256, id); err != nil {
			return manageFail(op, fmt.Errorf("publish payload: %w", err), s)
		}
		meta.Ciphertext = object
		if err := manageInjectFault("backup-uploaded"); err != nil {
			_ = backendDelete(backend, object)
			return manageFail(op, err, s)
		}
		if err := backendVerify(backend, object, meta.PayloadSHA256); err != nil {
			_ = backendDelete(backend, object)
			return manageFail(op, fmt.Errorf("verify published payload: %w", err), s)
		}
	}
	if remote {
		meta.MetadataEncryption = "gpg-public-key"
	}
	meta.State, meta.Complete = "verified", true
	if err := writeSnapshotMetaV2(stagingDir, &meta); err != nil {
		if remote {
			_ = backendDelete(backend, meta.Ciphertext)
		}
		return manageFail(op, err, s)
	}
	if err := verifySnapshotDirectoryV2(stagingDir, meta, meta.Encryption == "none"); err != nil {
		if remote {
			_ = backendDelete(backend, meta.Ciphertext)
		}
		return manageFail(op, fmt.Errorf("local snapshot verification: %w", err), s)
	}
	if remote {
		if err := publishRemoteMetadata(backend, meta, stagingDir, id, cfg.Backup.Encryption.Recipient); err != nil {
			cleanupRemoteBackup(backend, meta)
			return manageFail(op, fmt.Errorf("publish backup metadata: %w", err), s)
		}
		if err := verifyRemoteBackup(backend, meta, stagingDir); err != nil {
			cleanupRemoteBackup(backend, meta)
			return manageFail(op, fmt.Errorf("verify remote backup: %w", err), s)
		}
	}
	if _, err := os.Lstat(finalDir); err == nil {
		if remote {
			_ = backendDelete(backend, meta.Ciphertext)
		}
		return manageFail(op, errors.New("snapshot identity collision"), s)
	}
	if err := os.Rename(stagingDir, finalDir); err != nil {
		if remote {
			_ = backendDelete(backend, meta.Ciphertext)
		}
		return manageFail(op, fmt.Errorf("publish local snapshot: %w", err), s)
	}
	published = true
	op.Backend, op.Encryption = meta.Backend, meta.Encryption
	op.UploadState, op.VerificationState = meta.State, "verified"
	op.Status, op.CompletedAt, op.Snapshot = "ok", time.Now().UTC().Format(time.RFC3339Nano), id
	manageRecord(op, s)
	return manageOutput(meta)
}

func writeSnapshotMetaV2(dir string, meta *manageSnapshotMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	meta.ManifestSHA256 = hex.EncodeToString(digest[:])
	if err := atomicWriteFile(filepath.Join(dir, "manifest.json"), data, 0o600); err != nil {
		return err
	}
	return atomicWriteFile(filepath.Join(dir, "manifest.sha256"), []byte(hex.EncodeToString(digest[:])+"  manifest.json\n"), 0o600)
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".mema-write-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func managePrepareSnapshotV2(m manageManifest) (func() error, error) {
	if m.Snapshot.Consistency != "stop-service" {
		return nil, nil
	}
	var running []managedUnit
	declaredUnits := 0
	resume := func() error {
		var first error
		for i := len(running) - 1; i >= 0; i-- {
			item := running[i]
			if item.target.Type == "nginx" {
				if err := validateNginxTarget(item.target); err != nil && first == nil {
					first = err
				}
			}
			if err := runManageSystemctlForTarget(item.target, "start", item.unit); err != nil {
				if first == nil {
					first = fmt.Errorf("resume %s: %w", item.unit, err)
				}
				continue
			}
			if err := runManageSystemctlForTarget(item.target, "is-active", "--quiet", item.unit); err != nil && first == nil {
				first = fmt.Errorf("verify resumed %s: %w", item.unit, err)
			}
		}
		if first == nil && len(running) > 0 {
			if err := verifyManageHealth(m.Health); err != nil {
				first = fmt.Errorf("health after service resume: %w", err)
			}
		}
		return first
	}
	for _, target := range orderedManageTargets(m, true) {
		if target.Type != "systemd" && !(target.Type == "nginx" && len(target.Units) > 0) {
			continue
		}
		for _, unit := range target.Units {
			declaredUnits++
			if err := runManageSystemctlForTarget(target, "is-active", "--quiet", unit); err != nil {
				if isSystemdInactive(err) {
					continue
				}
				resumeErr := resume()
				return nil, errors.Join(fmt.Errorf("inspect %s before consistent snapshot: %w", unit, err), resumeErr)
			}
			item := managedUnit{target: target, unit: unit}
			running = append(running, item)
			if err := runManageSystemctlForTarget(target, "stop", unit); err != nil {
				resumeErr := resume()
				return nil, errors.Join(fmt.Errorf("stop %s for consistent snapshot: %w", unit, err), resumeErr)
			}
			if err := runManageSystemctlForTarget(target, "is-active", "--quiet", unit); !isSystemdInactive(err) {
				if err == nil {
					err = fmt.Errorf("%s remained active after stop", unit)
				}
				resumeErr := resume()
				return nil, errors.Join(fmt.Errorf("verify %s stopped: %w", unit, err), resumeErr)
			}
			result, err := systemdUnitResult(target, unit)
			if err != nil || result != "success" {
				if err == nil {
					err = fmt.Errorf("systemd reports stop result %q", result)
				}
				resumeErr := resume()
				return nil, errors.Join(fmt.Errorf("quiesce %s did not complete cleanly: %w", unit, err), resumeErr)
			}
		}
	}
	if declaredUnits == 0 {
		return nil, errors.New("stop-service consistency requires a declared systemd unit")
	}
	if len(running) == 0 {
		return nil, nil // already quiescent; preserve the existing inactive state.
	}
	return resume, nil
}

type managedUnit struct {
	target manageTarget
	unit   string
}

func orderedManageTargets(manifest manageManifest, reverse bool) []manageTarget {
	names := make([]string, 0, len(manifest.Targets))
	for name := range manifest.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	if reverse {
		for left, right := 0, len(names)-1; left < right; left, right = left+1, right-1 {
			names[left], names[right] = names[right], names[left]
		}
	}
	out := make([]manageTarget, 0, len(names))
	for _, name := range names {
		out = append(out, manifest.Targets[name])
	}
	return out
}

func managedSystemctlArgs(target manageTarget, args ...string) []string {
	if target.Scope == "user" {
		return append([]string{"--user"}, args...)
	}
	return args
}

func runManageSystemctlForTarget(target manageTarget, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "systemctl", managedSystemctlArgs(target, args...)...).Run()
}

func systemdUnitResult(target manageTarget, unit string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := managedSystemctlArgs(target, "show", "--property=Result", "--value", unit)
	output, err := exec.CommandContext(ctx, "systemctl", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func isSystemdInactive(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 3
}

func isSystemdInactiveOrMissing(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && (exitErr.ExitCode() == 3 || exitErr.ExitCode() == 4)
}

func validateNginxTarget(target manageTarget) error {
	binary := target.Binary
	args := []string{"-t"}
	if target.Config != "" {
		args = append(args, "-p", target.Prefix, "-c", target.Config)
	}
	if binary == "" {
		binary = "nginx"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("nginx configuration validation failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func inspectManageTargetStates(manifest manageManifest) map[string]map[string]string {
	states := map[string]map[string]string{}
	names := make([]string, 0, len(manifest.Targets))
	for name := range manifest.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := manifest.Targets[name]
		if target.Type != "systemd" && !(target.Type == "nginx" && len(target.Units) > 0) {
			continue
		}
		unitStates := map[string]string{}
		for _, unit := range target.Units {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			output, err := exec.CommandContext(ctx, "systemctl", managedSystemctlArgs(target, "is-active", unit)...).CombinedOutput()
			cancel()
			state := strings.TrimSpace(string(output))
			if state == "" && err != nil {
				state = "unavailable: " + err.Error()
			}
			unitStates[unit] = state
		}
		states[name] = unitStates
	}
	return states
}

func manageCaptureV2(src, dst, snapshotRoot string, excludes, excludeSockets []string) (manageCaptured, []manageExcluded, error) {
	return manageCaptureV2WithLstat(src, dst, snapshotRoot, excludes, excludeSockets, os.Lstat)
}

func manageCaptureV2WithLstat(src, dst, snapshotRoot string, excludes, excludeSockets []string, lstat func(string) (os.FileInfo, error)) (manageCaptured, []manageExcluded, error) {
	var excluded []manageExcluded
	rootInfo, err := lstat(src)
	if err != nil {
		return manageCaptured{}, nil, fmt.Errorf("required resource is unavailable: %w", err)
	}
	var walk func(string, string, string, string) (manageCaptured, error)
	walk = func(source, dest, snapshotRelative, resourceRelative string) (manageCaptured, error) {
		info, err := lstat(source)
		if err != nil {
			if resourceRelative != "" && os.IsNotExist(err) && matchesExactSnapshotExclusion(resourceRelative, excludeSockets) {
				excluded = append(excluded, manageExcluded{Path: source, Kind: "socket", Reason: "declared transient socket disappeared during capture"})
				return manageCaptured{}, errSnapshotExcluded
			}
			return manageCaptured{}, err
		}
		if resourceRelative != "" && matchesSnapshotExclusion(resourceRelative, excludes) {
			excluded = append(excluded, manageExcluded{Path: source, Reason: "manifest exclusion"})
			return manageCaptured{}, errSnapshotExcluded
		}
		if resourceRelative != "" && matchesExactSnapshotExclusion(resourceRelative, excludeSockets) {
			if info.Mode()&os.ModeSocket == 0 {
				return manageCaptured{}, fmt.Errorf("declared transient socket path is not a UNIX socket: %s", source)
			}
			excluded = append(excluded, manageExcluded{Path: source, Kind: "socket", Reason: "manifest transient socket exclusion"})
			return manageCaptured{}, errSnapshotExcluded
		}
		entry := manageCaptured{Path: source, SnapshotPath: filepath.ToSlash(snapshotRelative), Mode: uint32(info.Mode().Perm()), Kind: "file", Size: info.Size()}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			entry.UID, entry.GID = int(st.Uid), int(st.Gid)
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			entry.Kind = "symlink"
			entry.Link, err = os.Readlink(source)
			if err != nil {
				return entry, err
			}
			entry.Size = int64(len(entry.Link))
			if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
				return entry, err
			}
			if err := os.Symlink(entry.Link, dest); err != nil {
				return entry, err
			}
		case info.IsDir():
			entry.Kind = "directory"
			if err := os.MkdirAll(dest, 0o700); err != nil {
				return entry, err
			}
			children, err := os.ReadDir(source)
			if err != nil {
				return entry, err
			}
			for _, child := range children {
				childResourceRel := child.Name()
				if resourceRelative != "" {
					childResourceRel = filepath.ToSlash(filepath.Join(filepath.FromSlash(resourceRelative), child.Name()))
				}
				childSnapshotRel := filepath.ToSlash(filepath.Join(filepath.FromSlash(snapshotRelative), child.Name()))
				childEntry, err := walk(filepath.Join(source, child.Name()), filepath.Join(dest, child.Name()), childSnapshotRel, childResourceRel)
				if errors.Is(err, errSnapshotExcluded) {
					continue
				}
				if err != nil {
					return entry, err
				}
				entry.Entries = append(entry.Entries, childEntry)
			}
			if err := os.Chmod(dest, info.Mode().Perm()); err != nil {
				return entry, err
			}
		case info.Mode().IsRegular():
			if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
				return entry, err
			}
			if err := copyRegularFile(source, dest, info.Mode().Perm()); err != nil {
				return entry, err
			}
			entry.SHA256 = manageHash(source)
		default:
			return entry, fmt.Errorf("unsupported special file type at %s; declare an exclusion in the service manifest", source)
		}
		return entry, nil
	}
	captured, err := walk(src, dst, snapshotRoot, "")
	if err != nil {
		return captured, excluded, err
	}
	captured.Path = src
	captured.SnapshotPath = snapshotRoot
	_ = rootInfo
	return captured, excluded, nil
}

var errSnapshotExcluded = errors.New("snapshot resource intentionally excluded")

func matchesExactSnapshotExclusion(relative string, paths []string) bool {
	for _, candidate := range paths {
		if relative == candidate {
			return true
		}
	}
	return false
}

func matchesSnapshotExclusion(relative string, patterns []string) bool {
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "./") && strings.TrimPrefix(pattern, "./") == relative {
			return true
		}
		if pattern == relative || (!strings.Contains(pattern, "/") && pattern == pathpkg.Base(relative)) {
			return true
		}
		if matched, _ := pathpkg.Match(pattern, relative); matched {
			return true
		}
		if strings.HasPrefix(pattern, "**/") {
			if matched, _ := pathpkg.Match(strings.TrimPrefix(pattern, "**/"), pathpkg.Base(relative)); matched {
				return true
			}
		}
	}
	return false
}

func copyRegularFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if err := out.Chmod(mode); err != nil {
		_ = out.Close()
		return err
	}
	_, copyErr := io.Copy(out, in)
	if syncErr := out.Sync(); copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := out.Close(); copyErr == nil {
		copyErr = closeErr
	}
	return copyErr
}

func validateSnapshotResourceSet(resources []manageResource) error {
	paths := make([]string, 0, len(resources))
	for _, resource := range resources {
		if err := validateManagePath(resource.Path); err != nil {
			return err
		}
		paths = append(paths, filepath.Clean(resource.Path))
		for _, pattern := range resource.Exclude {
			unsafe := pattern == "" || strings.HasPrefix(pattern, "/") || strings.ContainsRune(pattern, '\\') || strings.ContainsRune(pattern, '\x00')
			if _, err := pathpkg.Match(pattern, "probe"); err != nil {
				unsafe = true
			}
			for _, component := range strings.Split(pattern, "/") {
				if component == ".." {
					unsafe = true
				}
			}
			if unsafe {
				return fmt.Errorf("invalid snapshot exclusion %q", pattern)
			}
		}
		seenSockets := map[string]bool{}
		for _, socketPath := range resource.ExcludeSockets {
			unsafe := socketPath == "" || strings.HasPrefix(socketPath, "/") || strings.ContainsAny(socketPath, "\\\x00*?[]") || filepath.ToSlash(filepath.Clean(filepath.FromSlash(socketPath))) != socketPath
			for _, component := range strings.Split(socketPath, "/") {
				if component == "" || component == "." || component == ".." {
					unsafe = true
				}
			}
			if unsafe || seenSockets[socketPath] {
				return fmt.Errorf("invalid snapshot socket exclusion %q", socketPath)
			}
			if matchesSnapshotExclusion(socketPath, resource.Exclude) {
				return fmt.Errorf("snapshot path %q has overlapping file and socket exclusions", socketPath)
			}
			seenSockets[socketPath] = true
		}
	}
	sort.Strings(paths)
	for i := 1; i < len(paths); i++ {
		if paths[i] == paths[i-1] || strings.HasPrefix(paths[i], paths[i-1]+string(filepath.Separator)) {
			return fmt.Errorf("overlapping snapshot resources %q and %q", paths[i-1], paths[i])
		}
	}
	return nil
}

func createSnapshotArchiveV2(snapshotDir, output string) error {
	out, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(out)
	tarWriter := tar.NewWriter(gz)
	closeAll := func() error {
		if err := tarWriter.Close(); err != nil {
			_ = gz.Close()
			_ = out.Close()
			return err
		}
		if err := gz.Close(); err != nil {
			_ = out.Close()
			return err
		}
		if err := out.Sync(); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	}
	walkErr := filepath.WalkDir(filepath.Join(snapshotDir, "filesystem"), func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(snapshotDir, p)
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(p)
			if err != nil {
				return err
			}
		}
		h, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		if info.Mode()&os.ModeSymlink != 0 {
			h.Typeflag = tar.TypeSymlink
		}
		h.Uid, h.Gid = 0, 0 // restore ownership comes from validated snapshot metadata, not archive headers.
		if err := tarWriter.WriteHeader(h); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(tarWriter, f)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	})
	if walkErr == nil {
		manifest := filepath.Join(snapshotDir, "service-manifest.json")
		f, err := os.Open(manifest)
		if err != nil {
			walkErr = err
		} else {
			info, statErr := f.Stat()
			if statErr != nil {
				walkErr = statErr
			} else {
				h, headerErr := tar.FileInfoHeader(info, "")
				if headerErr != nil {
					walkErr = headerErr
				} else {
					h.Name = "service-manifest.json"
					h.Uid, h.Gid = 0, 0
					if err := tarWriter.WriteHeader(h); err != nil {
						walkErr = err
					} else if _, err := io.Copy(tarWriter, f); err != nil {
						walkErr = err
					}
				}
			}
			if err := f.Close(); walkErr == nil {
				walkErr = err
			}
		}
	}
	closeErr := closeAll()
	if walkErr != nil {
		return walkErr
	}
	return closeErr
}

func writeSnapshotMetaWithDigest(dir string, meta *manageSnapshotMeta) error {
	return writeSnapshotMetaV2(dir, meta)
}

func snapshotManifestDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return snapshotSHA256(data), nil
}

func snapshotSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func manageEnsureSnapshotPayload(meta manageSnapshotMeta, dir string) (string, func(), error) {
	if meta.SnapshotFormat == 1 {
		return manageEnsureSnapshotPayloadV1(meta, dir)
	}
	if meta.SnapshotFormat != manageSnapshotFormatVersion {
		return "", func() {}, fmt.Errorf("unsupported snapshot format %d", meta.SnapshotFormat)
	}
	return ensureSnapshotPayloadV2(meta, dir)
}

func verifySnapshotPayloadV2(meta manageSnapshotMeta, root string, verifyMode bool) error {
	expected := map[string]bool{}
	if len(meta.Resources) > 0 {
		expected["resources"] = true
	}
	for _, top := range meta.Resources {
		for _, item := range flattenCaptured(top) {
			clean := filepath.Clean(filepath.FromSlash(item.SnapshotPath))
			if clean == "." || filepath.IsAbs(filepath.FromSlash(item.SnapshotPath)) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return fmt.Errorf("unsafe snapshot resource path %q", item.SnapshotPath)
			}
			if expected[clean] {
				return fmt.Errorf("duplicate snapshot resource path %q", clean)
			}
			expected[clean] = true
			p := filepath.Join(root, clean)
			if err := verifyCapturedAtMode(p, item, verifyMode); err != nil {
				return fmt.Errorf("snapshot resource %s: %w", item.Path, err)
			}
		}
	}
	actual := map[string]bool{}
	if err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		actual[filepath.Clean(rel)] = true
		return nil
	}); err != nil {
		return err
	}
	if len(actual) != len(expected) {
		return fmt.Errorf("snapshot filesystem member count mismatch: expected %d got %d", len(expected), len(actual))
	}
	for name := range actual {
		if !expected[name] {
			return fmt.Errorf("unexpected snapshot filesystem member %q", name)
		}
	}
	manifestPath := filepath.Join(filepath.Dir(root), "service-manifest.json")
	if digest := manageHash(manifestPath); digest == "" || (meta.ServiceManifestSHA256 != "" && digest != meta.ServiceManifestSHA256) {
		return errors.New("embedded service manifest checksum mismatch")
	}
	return nil
}

func verifyCapturedAtMode(path string, captured manageCaptured, verifyMode bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	kind := "file"
	if info.IsDir() {
		kind = "directory"
	}
	if info.Mode()&os.ModeSymlink != 0 {
		kind = "symlink"
	}
	if kind != captured.Kind {
		return fmt.Errorf("type mismatch (expected %s, got %s)", captured.Kind, kind)
	}
	if verifyMode && kind != "symlink" && uint32(info.Mode().Perm()) != captured.Mode&uint32(os.ModePerm) {
		return errors.New("mode mismatch")
	}
	if kind == "file" {
		if info.Size() != captured.Size || manageHash(path) != captured.SHA256 {
			return errors.New("size or content hash mismatch")
		}
	}
	if kind == "symlink" {
		link, err := os.Readlink(path)
		if err != nil || link != captured.Link || int64(len(link)) != captured.Size {
			return errors.New("symlink target mismatch")
		}
	}
	for _, child := range captured.Entries {
		childPath := filepath.Join(path, filepath.Base(filepath.FromSlash(child.SnapshotPath)))
		if err := verifyCapturedAtMode(childPath, child, verifyMode); err != nil {
			return err
		}
	}
	return nil
}

func verifySnapshotDirectoryV2(dir string, meta manageSnapshotMeta, inspectPayload bool) error {
	read, err := readSnapshotMeta(dir)
	if err != nil {
		return err
	}
	if read.ID != meta.ID || read.Service != meta.Service || !read.Complete || read.State != "verified" {
		return errors.New("snapshot metadata is incomplete or inconsistent")
	}
	if filepath.Base(meta.PayloadFile) != meta.PayloadFile || (meta.PayloadFile != "payload.tar.gz" && meta.PayloadFile != "payload.gpg") {
		return errors.New("snapshot payload filename is invalid")
	}
	payload, cleanupPayload, err := snapshotPayloadForVerification(meta, dir)
	if err != nil {
		return err
	}
	defer cleanupPayload()
	info, err := os.Stat(payload)
	if err != nil {
		return fmt.Errorf("snapshot payload missing: %w", err)
	}
	if info.Size() != meta.Size || manageHash(payload) != meta.PayloadSHA256 {
		return errors.New("snapshot payload size or checksum mismatch")
	}
	if meta.Encryption == "gpg-public-key" && meta.PayloadFile != "payload.gpg" {
		return errors.New("encrypted snapshot payload declaration is inconsistent")
	}
	if meta.Encryption == "none" && meta.PayloadFile != "payload.tar.gz" {
		return errors.New("plaintext snapshot payload declaration is inconsistent")
	}
	if !inspectPayload {
		return nil
	}
	root, cleanup, err := ensureSnapshotPayloadV2(meta, dir)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := verifySnapshotPayloadV2(meta, root, false); err != nil {
		return err
	}
	manifest, err := readEmbeddedManifest(root, meta)
	if err != nil {
		return err
	}
	return validateSnapshotResources(meta, manifest)
}

func snapshotPayloadForVerification(meta manageSnapshotMeta, dir string) (string, func(), error) {
	local := filepath.Join(dir, meta.PayloadFile)
	if _, err := os.Stat(local); err == nil {
		return local, func() {}, nil
	} else if !os.IsNotExist(err) {
		return "", func() {}, err
	}
	if meta.Encryption != "gpg-public-key" || meta.Ciphertext == "" {
		return "", func() {}, errors.New("snapshot payload is missing")
	}
	backend, err := manageBackend(meta.Backend)
	if err != nil {
		return "", func() {}, err
	}
	file, err := os.CreateTemp("", "mema-verify-payload-")
	if err != nil {
		return "", func() {}, err
	}
	path := file.Name()
	_ = file.Close()
	if err := backendGet(backend, meta.Ciphertext, path); err != nil {
		_ = os.Remove(path)
		return "", func() {}, err
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func manageVerifySnapshot(service, id string, s scope) error {
	dir := filepath.Join(manageStateDir(s), service, "snapshots", id)
	meta, err := readSnapshotMeta(dir)
	if err != nil {
		return err
	}
	if meta.Service != service || meta.ID != id {
		return errors.New("snapshot identity does not match request")
	}
	if meta.SnapshotFormat == 1 {
		root, cleanup, err := manageEnsureSnapshotPayloadV1(meta, dir)
		if err != nil {
			return err
		}
		defer cleanup()
		if err := verifySnapshotPayload(meta, root); err != nil {
			return err
		}
		return manageOutput(map[string]any{"snapshot": id, "service": service, "status": "verified", "format": 1, "compatibility": "legacy-v1"})
	}
	if meta.SnapshotFormat != manageSnapshotFormatVersion {
		return fmt.Errorf("unsupported snapshot format %d", meta.SnapshotFormat)
	}
	if err := verifySnapshotDirectoryV2(dir, meta, meta.Encryption == "none"); err != nil {
		return err
	}
	backend, err := manageBackend(meta.Backend)
	if err != nil {
		return err
	}
	if backend.Name != "local" || backend.Type != "local" {
		if err := verifyRemoteBackup(backend, meta, dir); err != nil {
			return err
		}
	}
	return manageOutput(map[string]any{"snapshot": id, "service": service, "status": "verified", "format": meta.SnapshotFormat, "payload_sha256": meta.PayloadSHA256, "encryption": meta.Encryption, "backend": meta.Backend})
}

func manageRestore(m manageManifest, manifestPath, service, id string, s scope) error {
	unlock, err := manageLock(service, s)
	if err != nil {
		return err
	}
	defer unlock()
	dir := filepath.Join(manageStateDir(s), service, "snapshots", id)
	meta, err := readSnapshotMeta(dir)
	if err != nil {
		return fmt.Errorf("snapshot %q not found or invalid: %w", id, err)
	}
	if meta.ID != id || meta.Service != service || !meta.Complete || meta.State != "verified" {
		return errors.New("snapshot identity does not match or is not complete")
	}
	op := manageOperation{ID: manageID("op"), Service: service, Type: "restore", Requested: id, Snapshot: id, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: "running"}
	manageRecord(op, s)
	root, cleanup, err := recoverRootFromSnapshot(meta, dir)
	if err != nil {
		return manageFail(op, err, s)
	}
	defer cleanup()
	if meta.SnapshotFormat == manageSnapshotFormatVersion {
		if err := verifySnapshotDirectoryV2(dir, meta, false); err != nil {
			return manageFail(op, err, s)
		}
	}
	if err := validateSnapshotResources(meta, m); err != nil {
		return manageFail(op, err, s)
	}
	embedded, err := readEmbeddedManifest(root, meta)
	if err != nil {
		return manageFail(op, err, s)
	}
	if !restoreManifestsCompatible(m, embedded, meta.SnapshotFormat) {
		return manageFail(op, errors.New("snapshot service manifest differs from installed service manifest beyond the snapshot-format policy"), s)
	}
	if err := manageApplyRestoreTransaction(meta, root, embedded); err != nil {
		return manageFail(op, err, s)
	}
	op.Status, op.CompletedAt = "ok", time.Now().UTC().Format(time.RFC3339Nano)
	manageRecord(op, s)
	return manageOutput(op)
}

func restoreManifestsCompatible(current, embedded manageManifest, snapshotFormat int) bool {
	if snapshotFormat != 1 && snapshotFormat != 2 {
		return false
	}
	expected := fmt.Sprintf("mema-snapshot-v%d", snapshotFormat)
	for _, format := range []string{current.Backup.Format, embedded.Backup.Format} {
		if format != "" && format != "mema-snapshot-v1" && format != "mema-snapshot-v2" {
			return false
		}
	}
	if embedded.Backup.Format != "" && embedded.Backup.Format != expected {
		return false
	}
	current.Backup.Format = ""
	embedded.Backup.Format = ""
	// Backup encoding/encryption policy affects new writes, not the decoding of
	// an existing artifact; its metadata selects how this snapshot is restored.
	current.Backup.Encryption = manageEncryptionPolicy{}
	embedded.Backup.Encryption = manageEncryptionPolicy{}
	// Exclusions affect future capture, while the snapshot resource tree records
	// exactly what this artifact contains. They do not change its restore map.
	current = clearManifestExclusions(current)
	embedded = clearManifestExclusions(embedded)
	currentBytes, currentErr := json.Marshal(current)
	embeddedBytes, embeddedErr := json.Marshal(embedded)
	return currentErr == nil && embeddedErr == nil && string(currentBytes) == string(embeddedBytes)
}

func clearManifestExclusions(manifest manageManifest) manageManifest {
	clear := func(resources []manageResource) []manageResource {
		resources = append([]manageResource(nil), resources...)
		for i := range resources {
			resources[i].Exclude = nil
			resources[i].ExcludeSockets = nil
		}
		return resources
	}
	manifest.Files = clear(manifest.Files)
	manifest.Config = clear(manifest.Config)
	manifest.Env = clear(manifest.Env)
	manifest.Data = clear(manifest.Data)
	manifest.Identity = clear(manifest.Identity)
	manifest.Binary = clear(manifest.Binary)
	manifest.Cleanup = clear(manifest.Cleanup)
	return manifest
}

func readEmbeddedManifest(root string, meta manageSnapshotMeta) (manageManifest, error) {
	var manifest manageManifest
	if meta.ServiceManifest != "service-manifest.json" {
		return manifest, errors.New("snapshot service-manifest path is invalid")
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(root), meta.ServiceManifest))
	if err != nil {
		return manifest, errors.New("snapshot does not contain an embedded service manifest")
	}
	if meta.ServiceManifestSHA256 != "" && manageHash(filepath.Join(filepath.Dir(root), meta.ServiceManifest)) != meta.ServiceManifestSHA256 {
		return manifest, errors.New("embedded service manifest checksum mismatch")
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		return manifest, err
	}
	if manifest.Service != meta.Service || manifest.Version != manageManifestVersion || manifest.Version != meta.ManifestVersion {
		return manifest, errors.New("embedded service manifest is incompatible")
	}
	if err := validateManageManifest(manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func flattenCaptured(root manageCaptured) []manageCaptured {
	out := []manageCaptured{root}
	for _, child := range root.Entries {
		out = append(out, flattenCaptured(child)...)
	}
	return out
}

func extractSnapshotV2(archive, destination string) ([]string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("open snapshot archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	types := map[string]byte{}
	var headers []*tar.Header
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		name := h.Name
		clean := pathpkg.Clean(name)
		if name == "" || strings.ContainsRune(name, '\x00') || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != strings.TrimSuffix(name, "/") {
			return nil, fmt.Errorf("snapshot archive contains unsafe path %q", name)
		}
		if _, ok := types[clean]; ok {
			return nil, fmt.Errorf("snapshot archive contains duplicate member %q", name)
		}
		switch h.Typeflag {
		case tar.TypeDir, tar.TypeReg, tar.TypeRegA, tar.TypeSymlink:
		default:
			return nil, fmt.Errorf("snapshot archive contains unsupported member type at %q", name)
		}
		if h.Typeflag == tar.TypeSymlink && (h.Linkname == "" || strings.ContainsRune(h.Linkname, '\x00')) {
			return nil, fmt.Errorf("snapshot archive contains invalid symlink %q", name)
		}
		types[clean] = h.Typeflag
		copyHeader := *h
		headers = append(headers, &copyHeader)
		if h.Typeflag == tar.TypeReg || h.Typeflag == tar.TypeRegA {
			if _, err := io.Copy(io.Discard, tr); err != nil {
				return nil, err
			}
		}
	}
	for name := range types {
		for parent := pathpkg.Dir(name); parent != "."; parent = pathpkg.Dir(parent) {
			if kind, ok := types[parent]; ok && kind != tar.TypeDir {
				return nil, fmt.Errorf("snapshot archive member %q is nested under non-directory %q", name, parent)
			}
		}
	}
	if _, ok := types["service-manifest.json"]; !ok || (types["service-manifest.json"] != tar.TypeReg && types["service-manifest.json"] != tar.TypeRegA) {
		return nil, errors.New("snapshot archive does not contain service manifest")
	}
	for _, h := range headers {
		name := h.Name
		target := filepath.Join(destination, filepath.FromSlash(pathpkg.Clean(name)))
		if !isPathWithin(destination, target) {
			return nil, errors.New("snapshot archive path escapes staging directory")
		}
		if h.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return nil, err
			}
		}
	}
	f2, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f2.Close()
	gz2, err := gzip.NewReader(f2)
	if err != nil {
		return nil, err
	}
	defer gz2.Close()
	tr2 := tar.NewReader(gz2)
	for {
		h, err := tr2.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		target := filepath.Join(destination, filepath.FromSlash(pathpkg.Clean(h.Name)))
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, err
		}
		switch h.Typeflag {
		case tar.TypeSymlink:
			if err := os.Symlink(h.Linkname, target); err != nil {
				return nil, err
			}
		case tar.TypeReg, tar.TypeRegA:
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return nil, err
			}
			_, copyErr := io.Copy(out, tr2)
			closeErr := out.Close()
			if copyErr != nil {
				return nil, copyErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
		}
	}
	members := make([]string, 0, len(types))
	for member := range types {
		members = append(members, member)
	}
	sort.Strings(members)
	return members, nil
}

func isPathWithin(root, candidate string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, candidateAbs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func ensureSnapshotPayloadV2(meta manageSnapshotMeta, dir string) (string, func(), error) {
	if meta.State != "verified" || !meta.Complete {
		return "", func() {}, errors.New("snapshot is not verified and recoverable")
	}
	if filepath.Base(meta.PayloadFile) != meta.PayloadFile || (meta.PayloadFile != "payload.tar.gz" && meta.PayloadFile != "payload.gpg") {
		return "", func() {}, errors.New("snapshot payload filename is invalid")
	}
	payload := filepath.Join(dir, meta.PayloadFile)
	cipherTemp := ""
	if _, err := os.Stat(payload); err != nil && meta.Encryption == "gpg-public-key" && meta.Ciphertext != "" {
		backend, beErr := manageBackend(meta.Backend)
		if beErr != nil {
			return "", func() {}, beErr
		}
		tmpCipher, err := os.CreateTemp("", "mema-ciphertext-")
		if err != nil {
			return "", func() {}, err
		}
		payload, cipherTemp = tmpCipher.Name(), tmpCipher.Name()
		_ = tmpCipher.Close()
		if err := backendGet(backend, meta.Ciphertext, payload); err != nil {
			_ = os.Remove(payload)
			return "", func() {}, err
		}
	}
	info, err := os.Stat(payload)
	if err != nil || info.Size() != meta.Size || manageHash(payload) != meta.PayloadSHA256 {
		if cipherTemp != "" {
			_ = os.Remove(cipherTemp)
		}
		return "", func() {}, errors.New("snapshot payload size or checksum mismatch")
	}
	if meta.Encryption == "none" {
		tmp, err := os.MkdirTemp("", "mema-recover-v2-")
		if err != nil {
			return "", func() {}, err
		}
		members, err := extractSnapshotV2(payload, tmp)
		if err != nil {
			_ = os.RemoveAll(tmp)
			return "", func() {}, err
		}
		if err := validateArchiveMembers(meta, members); err != nil {
			_ = os.RemoveAll(tmp)
			return "", func() {}, err
		}
		return filepath.Join(tmp, "filesystem"), func() { _ = os.RemoveAll(tmp) }, nil
	}
	if meta.Encryption != "gpg-public-key" {
		if cipherTemp != "" {
			_ = os.Remove(cipherTemp)
		}
		return "", func() {}, fmt.Errorf("unsupported snapshot encryption %q", meta.Encryption)
	}
	tmp, err := os.MkdirTemp("", "mema-recover-v2-")
	if err != nil {
		if cipherTemp != "" {
			_ = os.Remove(cipherTemp)
		}
		return "", func() {}, err
	}
	plain := filepath.Join(tmp, "payload.tar.gz")
	if err := decryptSnapshot(payload, plain); err != nil {
		_ = os.RemoveAll(tmp)
		if cipherTemp != "" {
			_ = os.Remove(cipherTemp)
		}
		return "", func() {}, fmt.Errorf("snapshot decryption failed (wrong or unavailable private key): %w", err)
	}
	members, err := extractSnapshotV2(plain, tmp)
	if err != nil {
		_ = os.RemoveAll(tmp)
		if cipherTemp != "" {
			_ = os.Remove(cipherTemp)
		}
		return "", func() {}, err
	}
	if err := validateArchiveMembers(meta, members); err != nil {
		_ = os.RemoveAll(tmp)
		if cipherTemp != "" {
			_ = os.Remove(cipherTemp)
		}
		return "", func() {}, err
	}
	return filepath.Join(tmp, "filesystem"), func() {
		_ = os.RemoveAll(tmp)
		if cipherTemp != "" {
			_ = os.Remove(cipherTemp)
		}
	}, nil
}

func validateArchiveMembers(meta manageSnapshotMeta, members []string) error {
	expected := map[string]bool{"filesystem": true, "service-manifest.json": true}
	if len(meta.Resources) > 0 {
		expected["filesystem/resources"] = true
	}
	for _, top := range meta.Resources {
		for _, entry := range flattenCaptured(top) {
			expected[filepath.ToSlash(filepath.Join("filesystem", filepath.FromSlash(entry.SnapshotPath)))] = true
		}
	}
	if len(expected) != len(members) {
		return fmt.Errorf("snapshot archive member count mismatch: expected %d got %d", len(expected), len(members))
	}
	for _, name := range members {
		if !expected[name] {
			return fmt.Errorf("unexpected snapshot archive member %q", name)
		}
	}
	return nil
}

func backendPublish(backend manageBackendConfig, object, source, digest, id string) error {
	if err := validateBackendObject(object); err != nil {
		return err
	}
	partial := object + ".partial-" + id
	if err := validateBackendObject(partial); err != nil {
		return err
	}
	switch backend.Type {
	case "local":
		if backend.Root == "" {
			return errors.New("local backend root is empty")
		}
		root, err := filepath.Abs(backend.Root)
		if err != nil {
			return err
		}
		destination := filepath.Join(root, filepath.FromSlash(object))
		staging := filepath.Join(root, filepath.FromSlash(partial))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		if _, err := os.Lstat(destination); err == nil {
			return errors.New("backup destination already exists")
		}
		defer os.Remove(staging)
		if err := copyFileExclusive(source, staging); err != nil {
			return err
		}
		if manageHash(staging) != digest {
			return errors.New("staged backup checksum mismatch")
		}
		if err := manageInjectFault("backup-before-promote"); err != nil {
			return err
		}
		if _, err := os.Lstat(destination); err == nil {
			return errors.New("backup destination already exists")
		}
		return os.Rename(staging, destination)
	case "ftp":
		defer backendDelete(backend, partial)
		if err := backendPut(backend, partial, source); err != nil {
			return err
		}
		if err := backendVerify(backend, partial, digest); err != nil {
			return err
		}
		if err := manageInjectFault("backup-before-promote"); err != nil {
			return err
		}
		return ftpRename(backend, partial, object)
	default:
		return fmt.Errorf("unsupported snapshot backend %q", backend.Type)
	}
}

func copyFileExclusive(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	if syncErr := out.Sync(); copyErr == nil {
		copyErr = syncErr
	}
	closeErr := out.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	return copyErr
}

func backendDelete(backend manageBackendConfig, object string) error {
	if err := validateBackendObject(object); err != nil {
		return err
	}
	switch backend.Type {
	case "local":
		return os.Remove(filepath.Join(backend.Root, filepath.FromSlash(object)))
	case "ftp":
		return ftpDelete(backend, object)
	default:
		return fmt.Errorf("unsupported snapshot backend %q", backend.Type)
	}
}

func publishRemoteMetadata(backend manageBackendConfig, meta manageSnapshotMeta, dir, id, recipient string) error {
	if err := manageInjectFault("backup-metadata-publish"); err != nil {
		return err
	}
	if recipient == "" || meta.MetadataEncryption != "gpg-public-key" {
		return errors.New("remote snapshot metadata requires public-key encryption")
	}
	manifest := filepath.Join(dir, "manifest.json")
	encryptedManifest := filepath.Join(dir, "manifest.gpg")
	if err := encryptSnapshot(manifest, encryptedManifest, recipient); err != nil {
		return fmt.Errorf("encrypt remote snapshot metadata: %w", err)
	}
	remoteManifest := meta.Ciphertext + ".manifest.gpg"
	return backendPublish(backend, remoteManifest, encryptedManifest, manageHash(encryptedManifest), id)
}

func verifyRemoteBackup(backend manageBackendConfig, meta manageSnapshotMeta, dir string) error {
	if meta.Ciphertext == "" {
		return errors.New("remote backup object is not recorded")
	}
	if err := backendVerify(backend, meta.Ciphertext, meta.PayloadSHA256); err != nil {
		return err
	}
	if meta.MetadataEncryption == "gpg-public-key" {
		localManifest := filepath.Join(dir, "manifest.gpg")
		expected := manageHash(localManifest)
		if expected == "" {
			return errors.New("encrypted local snapshot metadata is missing")
		}
		if err := backendVerify(backend, meta.Ciphertext+".manifest.gpg", expected); err != nil {
			return fmt.Errorf("remote encrypted metadata verification failed: %w", err)
		}
		return nil
	}
	if meta.MetadataEncryption != "" {
		return fmt.Errorf("unsupported snapshot metadata encryption %q", meta.MetadataEncryption)
	}
	// Historical snapshots retain their legacy plaintext metadata objects.
	for _, item := range []struct{ suffix, local string }{{".manifest.json", "manifest.json"}, {".manifest.sha256", "manifest.sha256"}} {
		expected, err := snapshotManifestDigest(filepath.Join(dir, item.local))
		if err != nil {
			return err
		}
		if err := backendVerify(backend, meta.Ciphertext+item.suffix, expected); err != nil {
			return fmt.Errorf("remote backup metadata verification failed: %w", err)
		}
	}
	return nil
}

func cleanupRemoteBackup(backend manageBackendConfig, meta manageSnapshotMeta) {
	if meta.Ciphertext == "" {
		return
	}
	_ = backendDelete(backend, meta.Ciphertext)
	_ = backendDelete(backend, meta.Ciphertext+".manifest.gpg")
	_ = backendDelete(backend, meta.Ciphertext+".manifest.json")
	_ = backendDelete(backend, meta.Ciphertext+".manifest.sha256")
}

func ftpRename(backend manageBackendConfig, from, to string) error {
	return ftpCommand(backend, []string{"RNFR " + from, "RNTO " + to})
}

func ftpDelete(backend manageBackendConfig, object string) error {
	return ftpCommand(backend, []string{"DELE " + object})
}

func ftpCommandRoot(backendURL string, commands []string) (string, []string, error) {
	if _, err := ftpNetrcMachine(backendURL); err != nil {
		return "", nil, err
	}
	parsed, err := url.Parse(backendURL)
	if err != nil {
		return "", nil, errors.New("FTP backend requires a credential-free base URL")
	}
	prefix := strings.Trim(parsed.Path, "/")
	if strings.Contains(prefix, "\\") {
		return "", nil, errors.New("FTP backend base path is invalid")
	}
	for _, part := range strings.Split(prefix, "/") {
		if part == "." || part == ".." {
			return "", nil, errors.New("FTP backend base path is invalid")
		}
	}
	rootURL := *parsed
	rootURL.Path, rootURL.RawPath = "/", ""
	rootURL.RawQuery, rootURL.Fragment = "", ""
	rewritten := make([]string, 0, len(commands))
	for _, command := range commands {
		verb, object, ok := strings.Cut(command, " ")
		if !ok || object == "" || (verb != "RNFR" && verb != "RNTO" && verb != "DELE") {
			return "", nil, errors.New("unsupported FTP control command")
		}
		if strings.ContainsAny(object, "\r\n\x00") || validateBackendObject(object) != nil {
			return "", nil, errors.New("unsafe FTP control object path")
		}
		if prefix != "" {
			object = pathpkg.Join(prefix, object)
		}
		if err := validateBackendObject(object); err != nil {
			return "", nil, err
		}
		rewritten = append(rewritten, verb+" "+object)
	}
	return rootURL.String(), rewritten, nil
}

func ftpCommand(backend manageBackendConfig, commands []string) error {
	host, err := ftpNetrcMachine(backend.URL)
	if err != nil {
		return err
	}
	controlURL, commands, err := ftpCommandRoot(backend.URL, commands)
	if err != nil {
		return err
	}
	netrc, err := os.CreateTemp("", "mema-ftp-netrc-")
	if err != nil {
		return err
	}
	defer os.Remove(netrc.Name())
	if err := netrc.Chmod(0o600); err != nil {
		_ = netrc.Close()
		return err
	}
	if backend.Username != "" {
		password := ""
		if backend.PasswordFile != "" {
			b, err := os.ReadFile(backend.PasswordFile)
			if err != nil {
				_ = netrc.Close()
				return err
			}
			password = strings.TrimSpace(string(b))
		}
		if _, err := fmt.Fprintf(netrc, "machine %s login %s password %s\n", host, backend.Username, password); err != nil {
			_ = netrc.Close()
			return err
		}
	}
	if err := netrc.Close(); err != nil {
		return err
	}
	args := []string{"-q", "--fail", "--silent", "--show-error", "--netrc-file", netrc.Name()}
	if backend.TLS {
		args = append(args, "--ftp-ssl")
	}
	for _, command := range commands {
		args = append(args, "--quote", command)
	}
	args = append(args, strings.TrimRight(controlURL, "/")+"/")
	cmd := exec.Command("curl", args...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		return errors.New("FTP snapshot promotion/deletion failed")
	}
	return nil
}

func runManageSystemctl(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "systemctl", args...).Run()
}

func manageApplyTargetsV2(manifest manageManifest, phase string) error {
	if phase == "stop" {
		var stopped []managedUnit
		for _, target := range orderedManageTargets(manifest, true) {
			if target.Type != "systemd" && !(target.Type == "nginx" && len(target.Units) > 0) {
				continue
			}
			for _, unit := range target.Units {
				if err := runManageSystemctlForTarget(target, "is-active", "--quiet", unit); err != nil {
					if isSystemdInactiveOrMissing(err) {
						continue
					}
					return errors.Join(fmt.Errorf("inspect service %s before restore: %w", unit, err), restartManagedUnits(stopped))
				}
				item := managedUnit{target: target, unit: unit}
				stopped = append(stopped, item)
				if err := runManageSystemctlForTarget(target, "stop", unit); err != nil {
					return errors.Join(fmt.Errorf("stop service %s: %w", unit, err), restartManagedUnits(stopped))
				}
				if err := runManageSystemctlForTarget(target, "is-active", "--quiet", unit); !isSystemdInactive(err) {
					if err == nil {
						err = fmt.Errorf("service %s remained active after stop", unit)
					}
					return errors.Join(fmt.Errorf("verify service %s stopped: %w", unit, err), restartManagedUnits(stopped))
				}
				result, err := systemdUnitResult(target, unit)
				if err != nil || result != "success" {
					if err == nil {
						err = fmt.Errorf("systemd reports stop result %q", result)
					}
					return errors.Join(fmt.Errorf("quiesce %s did not complete cleanly: %w", unit, err), restartManagedUnits(stopped))
				}
			}
		}
		return nil
	}
	ordered := orderedManageTargets(manifest, false)
	reloadedScopes := map[string]bool{}
	for _, target := range ordered {
		if target.Type != "systemd" && !(target.Type == "nginx" && len(target.Units) > 0) {
			continue
		}
		scope := target.Scope
		if scope == "" {
			scope = "system"
		}
		if !reloadedScopes[scope] {
			if err := runManageSystemctlForTarget(target, "daemon-reload"); err != nil {
				return fmt.Errorf("reload %s systemd configuration: %w", scope, err)
			}
			reloadedScopes[scope] = true
		}
	}
	for _, target := range ordered {
		switch target.Type {
		case "systemd":
			for _, unit := range target.Units {
				if err := runManageSystemctlForTarget(target, "start", unit); err != nil {
					return fmt.Errorf("start %s: %w", unit, err)
				}
				if err := runManageSystemctlForTarget(target, "is-active", "--quiet", unit); err != nil {
					return fmt.Errorf("service %s is not active: %w", unit, err)
				}
			}
		case "nginx":
			if err := validateNginxTarget(target); err != nil {
				return err
			}
			if len(target.Units) == 0 {
				if err := runManageSystemctlForTarget(target, "reload", "nginx"); err != nil {
					return fmt.Errorf("reload nginx: %w", err)
				}
				continue
			}
			for _, unit := range target.Units {
				if err := runManageSystemctlForTarget(target, "is-active", "--quiet", unit); err != nil {
					if !isSystemdInactive(err) {
						return fmt.Errorf("inspect nginx unit %s: %w", unit, err)
					}
					if err := runManageSystemctlForTarget(target, "start", unit); err != nil {
						return fmt.Errorf("start nginx unit %s: %w", unit, err)
					}
				} else if err := runManageSystemctlForTarget(target, "reload", unit); err != nil {
					return fmt.Errorf("reload nginx unit %s: %w", unit, err)
				}
				if err := runManageSystemctlForTarget(target, "is-active", "--quiet", unit); err != nil {
					return fmt.Errorf("nginx unit %s is not active: %w", unit, err)
				}
			}
		}
	}
	return nil
}

func restartManagedUnits(units []managedUnit) error {
	var first error
	for i := len(units) - 1; i >= 0; i-- {
		item := units[i]
		if item.target.Type == "nginx" {
			if err := validateNginxTarget(item.target); err != nil && first == nil {
				first = err
			}
		}
		if err := runManageSystemctlForTarget(item.target, "start", item.unit); err != nil && first == nil {
			first = fmt.Errorf("start %s: %w", item.unit, err)
		}
	}
	return first
}

func recoverRootFromSnapshot(meta manageSnapshotMeta, dir string) (string, func(), error) {
	if meta.SnapshotFormat == 1 {
		return manageEnsureSnapshotPayloadV1(meta, dir)
	}
	return ensureSnapshotPayloadV2(meta, dir)
}

func manageApplyRestoreTransaction(meta manageSnapshotMeta, root string, manifest manageManifest) error {
	if err := validateSnapshotResources(meta, manifest); err != nil {
		return err
	}
	if err := validateSnapshotResourceSet(manageResources(manifest)); err != nil {
		return err
	}
	if err := verifySnapshotPayload(meta, root); err != nil {
		return err
	}
	if err := validateRestoreOwnership(meta.Resources); err != nil {
		return err
	}
	if err := validateRestoreDestinations(meta.Resources); err != nil {
		return err
	}
	staged := make([]restoreStage, 0, len(meta.Resources))
	cleanupStages := func() {
		for _, item := range staged {
			_ = os.RemoveAll(item.transactionDir)
		}
	}
	for _, resource := range meta.Resources {
		if err := manageInjectFault("restore-stage"); err != nil {
			cleanupStages()
			return fmt.Errorf("restore staging interrupted: %w", err)
		}
		destination := resource.Path
		parent := filepath.Dir(destination)
		if err := os.MkdirAll(parent, 0o755); err != nil {
			cleanupStages()
			return err
		}
		txDir, err := os.MkdirTemp(parent, ".mema-restore-*")
		if err != nil {
			cleanupStages()
			return err
		}
		item := restoreStage{destination: destination, transactionDir: txDir, staged: filepath.Join(txDir, "new"), previous: filepath.Join(txDir, "old")}
		source := filepath.Join(root, filepath.FromSlash(resource.SnapshotPath))
		if err := copyManaged(source, item.staged); err != nil {
			_ = os.RemoveAll(txDir)
			cleanupStages()
			return fmt.Errorf("stage %s: %w", destination, err)
		}
		if err := applyCapturedMetadata(item.staged, resource); err != nil {
			_ = os.RemoveAll(txDir)
			cleanupStages()
			return fmt.Errorf("stage metadata for %s: %w", destination, err)
		}
		if err := verifyCapturedAt(item.staged, resource); err != nil {
			_ = os.RemoveAll(txDir)
			cleanupStages()
			return fmt.Errorf("staged tree verification for %s: %w", destination, err)
		}
		staged = append(staged, item)
	}
	if err := manageApplyTargetsV2(manifest, "stop"); err != nil {
		cleanupStages()
		return fmt.Errorf("stop service before restore: %w", err)
	}
	touched := 0
	rollback := func(cause error) error {
		_ = manageApplyTargetsV2(manifest, "stop")
		var rollbackErr error
		for i := touched - 1; i >= 0; i-- {
			item := &staged[i]
			if item.promoted {
				if item.hadPrevious {
					if err := os.RemoveAll(item.destination); err != nil && rollbackErr == nil {
						rollbackErr = err
					}
				} else if err := os.Rename(item.destination, item.staged); err != nil && rollbackErr == nil {
					rollbackErr = fmt.Errorf("retain newly restored %s for diagnosis: %w", item.destination, err)
				}
			}
			if item.hadPrevious {
				if err := os.Rename(item.previous, item.destination); err != nil && rollbackErr == nil {
					rollbackErr = err
				}
			}
		}
		if err := manageApplyTargetsV2(manifest, "activate"); err != nil && rollbackErr == nil {
			rollbackErr = err
		}
		if rollbackErr == nil {
			if err := verifyManageHealth(manifest.Health); err != nil {
				rollbackErr = fmt.Errorf("previous service health check: %w", err)
			}
		}
		if rollbackErr != nil {
			return fmt.Errorf("restore failed: %v; automatic rollback failed; recovery data retained in transaction directories: %w", cause, rollbackErr)
		}
		cleanupStages()
		return fmt.Errorf("restore failed; previous state and health restored: %w", cause)
	}
	if err := manageInjectFault("restore-before-promote"); err != nil {
		resumeErr := manageApplyTargetsV2(manifest, "activate")
		if resumeErr == nil {
			resumeErr = verifyManageHealth(manifest.Health)
		}
		cleanupStages()
		if resumeErr != nil {
			return fmt.Errorf("restore failed before promotion; service reactivation/health failed: %v; cause: %w", resumeErr, err)
		}
		return fmt.Errorf("restore failed before promotion; live state and health unchanged: %w", err)
	}
	for i := range staged {
		item := &staged[i]
		if _, err := os.Lstat(item.destination); err == nil {
			if err := os.Rename(item.destination, item.previous); err != nil {
				return rollback(fmt.Errorf("preserve previous %s: %w", item.destination, err))
			}
			item.hadPrevious = true
			touched = i + 1
		} else if !os.IsNotExist(err) {
			return rollback(fmt.Errorf("inspect destination %s: %w", item.destination, err))
		}
		if err := os.Rename(item.staged, item.destination); err != nil {
			return rollback(fmt.Errorf("promote restored %s: %w", item.destination, err))
		}
		item.promoted = true
		touched = i + 1
	}
	if err := manageInjectFault("restore-after-promote"); err != nil {
		return rollback(fmt.Errorf("injected failure after promotion: %w", err))
	}
	if err := manageApplyTargetsV2(manifest, "activate"); err != nil {
		return rollback(fmt.Errorf("activate restored service: %w", err))
	}
	if err := verifyManageHealth(manifest.Health); err != nil {
		return rollback(fmt.Errorf("post-restore health verification: %w", err))
	}
	cleanupStages()
	return nil
}

type restoreStage struct {
	destination    string
	transactionDir string
	staged         string
	previous       string
	hadPrevious    bool
	promoted       bool
}

func validateRestoreOwnership(resources []manageCaptured) error {
	if os.Geteuid() == 0 {
		return nil
	}
	uid, gid := os.Geteuid(), os.Getegid()
	for _, top := range resources {
		for _, entry := range flattenCaptured(top) {
			if entry.UID != uid || entry.GID != gid {
				return fmt.Errorf("cannot restore ownership for %s without root privileges", entry.Path)
			}
		}
	}
	return nil
}

func validateRestoreDestinations(resources []manageCaptured) error {
	for _, top := range resources {
		if !filepath.IsAbs(top.Path) || filepath.Clean(top.Path) != top.Path || top.Path == string(filepath.Separator) {
			return fmt.Errorf("unsafe restore destination %q", top.Path)
		}
		for parent := filepath.Dir(top.Path); parent != string(filepath.Separator); parent = filepath.Dir(parent) {
			info, err := os.Lstat(parent)
			if err == nil && info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("restore destination has symlink parent %q", parent)
			}
			if err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func applyCapturedMetadata(path string, captured manageCaptured) error {
	if captured.Kind == "symlink" {
		if os.Geteuid() == 0 {
			return os.Lchown(path, captured.UID, captured.GID)
		}
		return nil
	}
	if captured.Kind == "directory" {
		if err := os.Chmod(path, 0o700); err != nil {
			return err
		}
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(path, captured.UID, captured.GID); err != nil {
			return err
		}
	}
	for _, child := range captured.Entries {
		childPath := filepath.Join(path, filepath.Base(filepath.FromSlash(child.SnapshotPath)))
		if err := applyCapturedMetadata(childPath, child); err != nil {
			return err
		}
	}
	return os.Chmod(path, os.FileMode(captured.Mode)&os.ModePerm)
}

func verifyCapturedAt(path string, captured manageCaptured) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	kind := "file"
	if info.IsDir() {
		kind = "directory"
	}
	if info.Mode()&os.ModeSymlink != 0 {
		kind = "symlink"
	}
	if kind != captured.Kind || (kind != "symlink" && uint32(info.Mode().Perm()) != captured.Mode&uint32(os.ModePerm)) {
		return fmt.Errorf("type or mode mismatch at %s", path)
	}
	if kind == "file" {
		if (captured.Size > 0 && info.Size() != captured.Size) || manageHash(path) != captured.SHA256 {
			return fmt.Errorf("size or content mismatch at %s", path)
		}
	}
	if kind == "symlink" {
		link, err := os.Readlink(path)
		if err != nil || link != captured.Link {
			return fmt.Errorf("symlink mismatch at %s", path)
		}
	}
	for _, child := range captured.Entries {
		childPath := filepath.Join(path, filepath.Base(filepath.FromSlash(child.SnapshotPath)))
		if err := verifyCapturedAt(childPath, child); err != nil {
			return err
		}
	}
	return nil
}

func verifyManageHealth(health manageHealth) error {
	client := &http.Client{Timeout: time.Second}
	checks := []struct{ label, url string }{{"ready", health.Ready}, {"version", health.Version}}
	for _, check := range checks {
		if check.url == "" {
			continue
		}
		deadline := time.Now().Add(8 * time.Second)
		var lastErr error
		for time.Now().Before(deadline) {
			resp, err := client.Get(check.url)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					lastErr = nil
					break
				}
				lastErr = fmt.Errorf("returned %s", resp.Status)
			} else {
				lastErr = err
			}
			time.Sleep(200 * time.Millisecond)
		}
		if lastErr != nil {
			return fmt.Errorf("%s health check did not pass before deadline: %w", check.label, lastErr)
		}
	}
	return nil
}
