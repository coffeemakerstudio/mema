package main

// Generic, manifest-driven service lifecycle management.  Applications opt in
// by installing a versioned JSON manifest; the engine never guesses paths or
// service names from the caller.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const manageManifestVersion = 1

type manageResource struct {
	Path     string `json:"path"`
	Role     string `json:"role,omitempty"`
	Type     string `json:"type,omitempty"`
	Secret   bool   `json:"secret,omitempty"`
	Critical bool   `json:"critical,omitempty"`
	Clean    string `json:"clean,omitempty"` // never, quarantine, temporary, managed-history
}

type manageTarget struct {
	Type    string   `json:"type"`
	Units   []string `json:"units,omitempty"`
	Configs []string `json:"configs,omitempty"`
}

type manageHealth struct {
	Ready   string `json:"ready,omitempty"`
	Version string `json:"version,omitempty"`
}

type manageSnapshotPolicy struct {
	Consistency string `json:"consistency,omitempty"`
	Backend     string `json:"backend,omitempty"`
	Recipient   string `json:"recipient,omitempty"`
}
type manageEncryptionPolicy struct {
	Type      string `json:"type,omitempty"`
	Recipient string `json:"recipient,omitempty"`
}
type manageCompressionPolicy struct {
	Type  string `json:"type,omitempty"`
	Level string `json:"level,omitempty"`
}
type manageBackupPolicy struct {
	Format      string                  `json:"format,omitempty"`
	Encryption  manageEncryptionPolicy  `json:"encryption,omitempty"`
	Compression manageCompressionPolicy `json:"compression,omitempty"`
	Backend     string                  `json:"backend,omitempty"`
	File        string                  `json:"file,omitempty"`
	Verify      struct {
		RemoteHash bool `json:"remote_hash,omitempty"`
	} `json:"verify,omitempty"`
}
type manageCleanPolicy struct {
	GracePeriod string `json:"grace_period,omitempty"`
}

type manageManifest struct {
	Version   int                       `json:"version"`
	Service   string                    `json:"service"`
	Files     []manageResource          `json:"files,omitempty"`
	Config    []manageResource          `json:"config,omitempty"`
	Env       []manageResource          `json:"env,omitempty"`
	Data      []manageResource          `json:"data,omitempty"`
	Identity  []manageResource          `json:"identity,omitempty"`
	Binary    []manageResource          `json:"binary,omitempty"`
	Cleanup   []manageResource          `json:"cleanup,omitempty"`
	Targets   map[string]manageTarget   `json:"targets,omitempty"`
	Health    manageHealth              `json:"health,omitempty"`
	Snapshot  manageSnapshotPolicy      `json:"snapshot,omitempty"`
	Backup    manageBackupPolicy        `json:"backup,omitempty"`
	Variables map[string]manageVariable `json:"variables,omitempty"`
	Clean     manageCleanPolicy         `json:"clean,omitempty"`
}

const manageSnapshotFormatVersion = 1
const manageEngineVersion = "0.2"

type manageSnapshotMeta struct {
	SnapshotFormat      int              `json:"snapshot_format"`
	ID                  string           `json:"id"`
	ManifestVersion     int              `json:"manifest_version"`
	MemaVersion         string           `json:"mema_version"`
	Service             string           `json:"service"`
	CreatedAt           string           `json:"created_at"`
	Host                string           `json:"source_host"`
	Consistency         string           `json:"consistency"`
	Complete            bool             `json:"complete"`
	State               string           `json:"state"`
	Backend             string           `json:"backend,omitempty"`
	BackupFile          string           `json:"backup_file,omitempty"`
	Encryption          string           `json:"encryption,omitempty"`
	EncryptionRecipient string           `json:"encryption_recipient,omitempty"`
	Compression         string           `json:"compression,omitempty"`
	Ciphertext          string           `json:"ciphertext,omitempty"`
	CiphertextSHA256    string           `json:"ciphertext_sha256,omitempty"`
	Size                int64            `json:"size,omitempty"`
	ApplicationRevision string           `json:"application_revision,omitempty"`
	ApplicationVersion  string           `json:"application_version,omitempty"`
	Schema              string           `json:"schema,omitempty"`
	ServiceManifest     string           `json:"service_manifest,omitempty"`
	Resources           []manageCaptured `json:"resources"`
}

type manageCaptured struct {
	Path         string `json:"path"`
	SnapshotPath string `json:"snapshot_path"`
	Kind         string `json:"kind"`
	Mode         uint32 `json:"mode"`
	UID          int    `json:"uid,omitempty"`
	GID          int    `json:"gid,omitempty"`
	Link         string `json:"link,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
}

type manageOperation struct {
	ID                string `json:"id"`
	Service           string `json:"service"`
	Type              string `json:"type"`
	Requested         string `json:"requested,omitempty"`
	Snapshot          string `json:"snapshot,omitempty"`
	StartedAt         string `json:"started_at"`
	CompletedAt       string `json:"completed_at,omitempty"`
	Status            string `json:"status"`
	Error             string `json:"error,omitempty"`
	Backend           string `json:"backend,omitempty"`
	Encryption        string `json:"encryption,omitempty"`
	UploadState       string `json:"upload_state,omitempty"`
	VerificationState string `json:"verification_state,omitempty"`
}

type manageQuarantine struct {
	OperationID   string `json:"operation_id"`
	Service       string `json:"service"`
	Original      string `json:"original_path"`
	Quarantine    string `json:"quarantine_path"`
	SHA256        string `json:"sha256,omitempty"`
	Snapshot      string `json:"snapshot,omitempty"`
	QuarantinedAt string `json:"quarantined_at"`
	PurgeAfter    string `json:"purge_after"`
	Restored      bool   `json:"restored"`
}

func manageCommand(args []string, s scope) error {
	overrides, args, err := parseManageInvocation(args)
	if err != nil {
		return err
	}
	if len(args) < 2 {
		return errors.New("usage: mema manage <service> <status|verify|snapshot|snapshots|restore|update|rollback|clean|config|logs>")
	}
	service := args[0]
	if err := validatePathComponent(service, "service"); err != nil {
		return err
	}
	m, manifestPath, err := loadManageManifest(service, s)
	if err != nil {
		return err
	}
	if _, err := resolveManageConfig(m, manifestPath, service, s, overrides, map[string]string{"SERVICE": service, "HOST": hostnameOrUnknown(), "SNAPSHOT_ID": "snapshot-pending", "TIMESTAMP": time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		return err
	}
	op := args[1]
	if op == "clean" && len(args) > 2 {
		switch args[2] {
		case "full":
			return manageCleanWithOverrides(m, manifestPath, service, s, true, overrides)
		case "status":
			return manageCleanStatus(service, s)
		case "undo":
			if len(args) != 4 {
				return errors.New("usage: mema manage <service> clean undo <operation-id>")
			}
			return manageCleanUndo(service, args[3], s)
		default:
			return errors.New("usage: mema manage <service> clean [full|status|undo <operation-id>]")
		}
	}
	switch op {
	case "status":
		return manageStatus(m, manifestPath, service, s)
	case "verify":
		return manageVerify(m, service)
	case "snapshot":
		return manageSnapshotWithOverrides(m, manifestPath, service, s, overrides)
	case "snapshots":
		return manageSnapshots(service, s)
	case "restore":
		if len(args) != 3 {
			return errors.New("usage: mema manage <service> restore <snapshot>")
		}
		if err := validatePathComponent(args[2], "snapshot"); err != nil {
			return err
		}
		return manageRestore(m, manifestPath, service, args[2], s)
	case "update":
		return errors.New("manifest does not declare a generic update provider")
	case "rollback":
		return errors.New("no previous deployment is available")
	case "clean":
		return manageCleanWithOverrides(m, manifestPath, service, s, false, overrides)
	case "config":
		return manageConfigCommand(m, manifestPath, service, s, overrides)
	case "logs":
		return manageLogs(service, s)
	default:
		return fmt.Errorf("unknown manage operation %q", op)
	}
}

func manageManifestDirs(s scope) []string {
	if v := os.Getenv("MEMA_MANAGE_MANIFEST_DIR"); v != "" {
		return []string{v}
	}
	return []string{filepath.Join(s.recipeDir, "manage"), filepath.Join(globalRecipeDir, "manage")}
}
func manageStateDir(s scope) string {
	if v := os.Getenv("MEMA_MANAGE_STATE_DIR"); v != "" {
		return v
	}
	if s.global {
		return "/var/lib/mema/manage"
	}
	return filepath.Join(os.ExpandEnv(localInstallRoot), "manage-state")
}
func loadManageManifest(service string, s scope) (manageManifest, string, error) {
	var m manageManifest
	var path string
	for _, dir := range manageManifestDirs(s) {
		candidate := filepath.Join(dir, service+".json")
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
			break
		}
	}
	if path == "" {
		return m, "", fmt.Errorf("management capability for %q is not declared", service)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return m, "", fmt.Errorf("read management manifest: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return m, "", fmt.Errorf("parse management manifest: %w", err)
	}
	if m.Version != manageManifestVersion {
		return m, "", fmt.Errorf("unsupported management manifest version %d", m.Version)
	}
	if m.Service != service {
		return m, "", errors.New("management manifest service does not match filename")
	}
	if err := validateManageManifest(m); err != nil {
		return m, "", err
	}
	return m, path, nil
}
func validateManageManifest(m manageManifest) error {
	for _, r := range manageResources(m) {
		if err := validateManagePath(r.Path); err != nil {
			return err
		}
	}
	for _, t := range m.Targets {
		for _, unit := range t.Units {
			if unit == "" || strings.ContainsAny(unit, "/\\ ") {
				return fmt.Errorf("invalid systemd unit %q", unit)
			}
		}
		for _, p := range t.Configs {
			if err := validateManagePath(p); err != nil {
				return err
			}
		}
	}
	return nil
}
func validateManagePath(p string) error {
	if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.Contains(p, "\x00") {
		return fmt.Errorf("manifest path must be a clean absolute path: %q", p)
	}
	return nil
}
func manageResources(m manageManifest) []manageResource {
	var out []manageResource
	for _, group := range [][]manageResource{m.Files, m.Config, m.Env, m.Data, m.Identity, m.Binary, m.Cleanup} {
		for _, r := range group {
			if r.Clean == "" && containsResource(m.Identity, r.Path) {
				r.Clean = "never"
			}
			out = append(out, r)
		}
	}
	return out
}
func containsResource(rs []manageResource, p string) bool {
	for _, r := range rs {
		if r.Path == p {
			return true
		}
	}
	return false
}

func manageOutput(v any) error {
	data, _ := json.MarshalIndent(v, "", "  ")
	if manageJSON {
		fmt.Println(string(data))
		return nil
	}
	switch x := v.(type) {
	case manageOperation:
		fmt.Printf("%s %s: %s\n", x.Type, x.Service, x.Status)
		if x.Error != "" {
			fmt.Println(x.Error)
		}
	case manageSnapshotMeta:
		fmt.Printf("snapshot %s (%s)\n", x.ID, x.CreatedAt)
	default:
		fmt.Println(string(data))
	}
	return nil
}
func manageID(prefix string) string { return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()) }
func manageLock(service string, s scope) (func(), error) {
	dir := manageStateDir(s)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, service+".lock")
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, writeErr := fmt.Fprintf(f, "%d\n", os.Getpid())
			closeErr := f.Close()
			if writeErr != nil {
				_ = os.Remove(p)
				return nil, writeErr
			}
			if closeErr != nil {
				_ = os.Remove(p)
				return nil, closeErr
			}
			return func() { _ = os.Remove(p) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		data, readErr := os.ReadFile(p)
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if readErr != nil || parseErr != nil || pid <= 0 {
			return nil, fmt.Errorf("service %q has a lifecycle operation in progress", service)
		}
		if err := syscall.Kill(pid, 0); err == nil || err == syscall.EPERM {
			return nil, fmt.Errorf("service %q has a lifecycle operation in progress", service)
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove stale lifecycle lock: %w", err)
		}
	}
	return nil, fmt.Errorf("service %q has a lifecycle operation in progress", service)
}
func manageJournalPath(service string, s scope) string {
	return filepath.Join(manageStateDir(s), service, "operations.jsonl")
}
func manageRecord(op manageOperation, s scope) {
	p := manageJournalPath(op.Service, s)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err == nil {
		b, _ := json.Marshal(op)
		_, _ = f.Write(append(b, '\n'))
		_ = f.Close()
	}
}

func manageStatus(m manageManifest, manifestPath, service string, s scope) error {
	result := map[string]any{"service": service, "manifest": manifestPath, "manifest_version": m.Version, "resources": len(manageResources(m)), "targets": m.Targets}
	return manageOutput(result)
}
func manageVerify(m manageManifest, service string) error {
	missing := []string{}
	for _, r := range manageResources(m) {
		if _, err := os.Lstat(r.Path); err != nil && os.IsNotExist(err) {
			missing = append(missing, r.Path)
		}
	}
	checks := map[string]string{}
	client := &http.Client{Timeout: 5 * time.Second}
	for name, url := range map[string]string{"ready": m.Health.Ready, "version": m.Health.Version} {
		if url == "" {
			continue
		}
		resp, err := client.Get(url)
		if err != nil {
			checks[name] = err.Error()
			continue
		}
		_ = resp.Body.Close()
		checks[name] = resp.Status
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			missing = append(missing, "health:"+name)
		}
	}
	result := map[string]any{"service": service, "status": "ok", "missing": missing, "health": checks}
	if len(missing) > 0 {
		result["status"] = "degraded"
	}
	return manageOutput(result)
}

func manageSnapshot(m manageManifest, manifestPath, service string, s scope) error {
	return manageSnapshotWithOverrides(m, manifestPath, service, s, nil)
}
func manageSnapshotWithOverrides(m manageManifest, manifestPath, service string, s scope, overrides map[string]string) error {
	pending := map[string]string{"SERVICE": service, "SNAPSHOT_ID": "snapshot-pending", "TIMESTAMP": time.Now().UTC().Format(time.RFC3339Nano), "HOST": hostnameOrUnknown()}
	if _, err := resolveManageConfig(m, manifestPath, service, s, overrides, pending); err != nil {
		return err
	}
	unlock, err := manageLock(service, s)
	if err != nil {
		return err
	}
	defer unlock()
	op := manageOperation{ID: manageID("op"), Service: service, Type: "snapshot", StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: "running"}
	manageRecord(op, s)
	id := manageID("snap")
	dir := filepath.Join(manageStateDir(s), service, "snapshots", id)
	if err := os.MkdirAll(filepath.Join(dir, "filesystem"), 0o700); err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			_ = os.RemoveAll(filepath.Join(dir, "filesystem"))
			_ = os.Remove(filepath.Join(dir, ".payload.tar.gz"))
			_ = os.Remove(filepath.Join(dir, "payload.gpg"))
		}
	}()
	resume, err := managePrepareSnapshot(m)
	if err != nil {
		return manageFail(op, err, s)
	}
	defer resume()
	builtins := map[string]string{"SERVICE": service, "SNAPSHOT_ID": id, "TIMESTAMP": time.Now().UTC().Format(time.RFC3339Nano), "HOST": hostnameOrUnknown()}
	cfg, err := resolveManageConfig(m, manifestPath, service, s, overrides, builtins)
	if err != nil {
		return manageFail(op, err, s)
	}
	meta := manageSnapshotMeta{SnapshotFormat: manageSnapshotFormatVersion, MemaVersion: manageEngineVersion, ID: id, ManifestVersion: m.Version, Service: service, CreatedAt: builtins["TIMESTAMP"], Consistency: m.Snapshot.Consistency, State: "creating", ServiceManifest: "service-manifest.json", Complete: false}
	if meta.Consistency == "" {
		meta.Consistency = "live"
	}
	if h, err := os.Hostname(); err == nil {
		meta.Host = h
	}
	if err := writeSnapshotMeta(dir, meta); err != nil {
		return manageFail(op, err, s)
	}
	for _, r := range manageResources(m) {
		if _, err := os.Lstat(r.Path); os.IsNotExist(err) {
			continue
		}
		c, err := manageCapture(r.Path, filepath.Join(dir, "filesystem", strings.TrimPrefix(r.Path, "/")))
		if err != nil {
			return manageFail(op, err, s)
		}
		c.SnapshotPath = filepath.ToSlash(strings.TrimPrefix(r.Path, "/"))
		meta.Resources = append(meta.Resources, c)
	}
	if err := copyFile(manifestPath, filepath.Join(dir, "service-manifest.json")); err != nil {
		return manageFail(op, err, s)
	}
	meta.State = "captured"
	b, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o600); err != nil {
		return manageFail(op, err, s)
	}
	meta, err = manageFinalizeSnapshot(meta, dir, m, cfg, s)
	if err != nil {
		meta.State = "failed"
		meta.Complete = false
		_ = writeSnapshotMeta(dir, meta)
		return manageFail(op, err, s)
	}
	if err := writeSnapshotMeta(dir, meta); err != nil {
		return manageFail(op, err, s)
	}
	completed = true
	op.Backend = meta.Backend
	op.Encryption = meta.Encryption
	op.UploadState = meta.State
	op.VerificationState = meta.State
	op.Status = "ok"
	op.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	op.Snapshot = id
	manageRecord(op, s)
	return manageOutput(meta)
}
func managePrepareSnapshot(m manageManifest) (func(), error) {
	if m.Snapshot.Consistency != "stop-service" {
		return func() {}, nil
	}
	var running []string
	for _, target := range m.Targets {
		if target.Type != "systemd" {
			continue
		}
		for _, unit := range target.Units {
			if exec.Command("systemctl", "is-active", "--quiet", unit).Run() == nil {
				if err := exec.Command("systemctl", "stop", unit).Run(); err != nil {
					return nil, fmt.Errorf("stop %s: %w", unit, err)
				}
				running = append(running, unit)
			}
		}
	}
	return func() {
		for _, unit := range running {
			_ = exec.Command("systemctl", "start", unit).Run()
		}
	}, nil
}

func manageCapture(src, dst string) (manageCaptured, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return manageCaptured{}, err
	}
	c := manageCaptured{Path: src, SnapshotPath: strings.TrimPrefix(dst, "/"), Mode: uint32(info.Mode()), Kind: "file"}
	if info.IsDir() {
		c.Kind = "directory"
	}
	if info.Mode()&os.ModeSymlink != 0 {
		c.Kind = "symlink"
		c.Link, err = os.Readlink(src)
		if err != nil {
			return c, err
		}
	}
	if err = copyManaged(src, dst); err != nil {
		return c, err
	}
	if c.Kind == "file" {
		c.SHA256 = manageHash(src)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		c.UID = int(st.Uid)
		c.GID = int(st.Gid)
	}
	return c, nil
}
func copyManaged(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		link, e := os.Readlink(src)
		if e != nil {
			return e
		}
		return os.Symlink(link, dst)
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, e := os.ReadDir(src)
		if e != nil {
			return e
		}
		for _, x := range entries {
			if e := copyManaged(filepath.Join(src, x.Name()), filepath.Join(dst, x.Name())); e != nil {
				return e
			}
		}
		return os.Chmod(dst, info.Mode().Perm())
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if e != nil {
		return e
	}
	_, e = io.Copy(out, in)
	if ce := out.Close(); e == nil {
		e = ce
	}
	return e
}
func manageHash(p string) string {
	f, e := os.Open(p)
	if e != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	_, _ = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil))
}
func manageFail(op manageOperation, err error, s scope) error {
	op.Status = "failed"
	op.Error = err.Error()
	op.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	manageRecord(op, s)
	return err
}

func manageSnapshots(service string, s scope) error {
	root := filepath.Join(manageStateDir(s), service, "snapshots")
	entries, _ := os.ReadDir(root)
	out := []manageSnapshotMeta{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(root, e.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var m manageSnapshotMeta
		if json.Unmarshal(b, &m) == nil {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	if manageJSON {
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	for _, m := range out {
		fmt.Printf("%s\t%s\tstate=%s\tbackend=%s\tencryption=%s\tsize=%d\n", m.ID, m.CreatedAt, m.State, m.Backend, m.Encryption, m.Size)
	}
	return nil
}

func manageRestore(m manageManifest, manifestPath, service, id string, s scope) error {
	unlock, err := manageLock(service, s)
	if err != nil {
		return err
	}
	defer unlock()
	dir := filepath.Join(manageStateDir(s), service, "snapshots", id)
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("snapshot %q not found", id)
	}
	var meta manageSnapshotMeta
	if err = json.Unmarshal(b, &meta); err != nil || !meta.Complete {
		return errors.New("snapshot is incomplete or invalid")
	}
	op := manageOperation{ID: manageID("op"), Service: service, Type: "restore", Requested: id, Snapshot: id, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: "running"}
	manageRecord(op, s)
	root, cleanupPayload, err := manageEnsureSnapshotPayload(meta, dir)
	if err != nil {
		return manageFail(op, err, s)
	}
	defer cleanupPayload()
	if err := validateSnapshotResources(meta, m); err != nil {
		return manageFail(op, err, s)
	}
	if err := verifySnapshotPayload(meta, root); err != nil {
		return manageFail(op, err, s)
	}
	if err := manageApplyTargets(m, "stop"); err != nil {
		return manageFail(op, err, s)
	}
	for _, c := range meta.Resources {
		src := filepath.Join(root, filepath.FromSlash(c.SnapshotPath))
		if err := manageRestoreOne(src, c.Path, c); err != nil {
			return manageFail(op, err, s)
		}
	}
	if err := manageApplyTargets(m, "activate"); err != nil {
		return manageFail(op, err, s)
	}
	op.Status = "ok"
	op.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	manageRecord(op, s)
	return manageOutput(op)
}
func manageRestoreOne(src, dst string, c manageCaptured) error {
	_ = os.RemoveAll(dst)
	if err := copyManaged(src, dst); err != nil {
		return err
	}
	if c.Kind == "symlink" {
		return nil
	}
	if err := os.Chmod(dst, os.FileMode(c.Mode)&os.ModePerm); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		_ = os.Chown(dst, c.UID, c.GID)
	}
	return nil
}
func manageApplyTargets(m manageManifest, phase string) error {
	for _, t := range m.Targets {
		switch t.Type {
		case "systemd":
			if phase == "stop" {
				for _, u := range t.Units {
					_ = exec.Command("systemctl", "stop", u).Run()
				}
			} else {
				if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
					return fmt.Errorf("reload systemd configuration: %w", err)
				}
				for _, u := range t.Units {
					if err := exec.Command("systemctl", "start", u).Run(); err != nil {
						return fmt.Errorf("start %s: %w", u, err)
					}
					if err := exec.Command("systemctl", "is-active", "--quiet", u).Run(); err != nil {
						return fmt.Errorf("service %s is not active: %w", u, err)
					}
				}
			}
		case "nginx":
			if phase == "activate" {
				if err := exec.Command("nginx", "-t").Run(); err != nil {
					return fmt.Errorf("nginx validation failed: %w", err)
				}
				if err := exec.Command("systemctl", "reload", "nginx").Run(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func manageClean(m manageManifest, manifestPath, service string, s scope, full bool) error {
	return manageCleanWithOverrides(m, manifestPath, service, s, full, nil)
}
func manageCleanWithOverrides(m manageManifest, manifestPath, service string, s scope, full bool, overrides map[string]string) error {
	if _, err := resolveManageConfig(m, manifestPath, service, s, overrides, map[string]string{"SERVICE": service, "HOST": hostnameOrUnknown(), "SNAPSHOT_ID": "snapshot-pending", "TIMESTAMP": time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		return err
	}
	unlock, err := manageLock(service, s)
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := resolveManageConfig(m, manifestPath, service, s, overrides, map[string]string{"SERVICE": service, "HOST": hostnameOrUnknown(), "SNAPSHOT_ID": "snapshot-pending", "TIMESTAMP": time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return err
	}
	grace := cfg.CleanGrace
	items := []manageQuarantine{}
	if existing, readErr := manageReadQuarantine(service, s); readErr == nil {
		items = append(items, existing...)
	} else {
		return readErr
	}
	newCount := 0
	for _, r := range m.Cleanup {
		if r.Critical || r.Clean == "never" {
			continue
		}
		if _, e := os.Lstat(r.Path); e != nil {
			continue
		}
		q := filepath.Join(manageStateDir(s), service, "quarantine", manageID("q"), strings.TrimPrefix(r.Path, "/"))
		if err := os.MkdirAll(filepath.Dir(q), 0o700); err != nil {
			return err
		}
		if err := os.Rename(r.Path, q); err != nil {
			if !errors.Is(err, syscall.EXDEV) {
				return err
			}
			if err := copyManaged(r.Path, q); err != nil {
				return err
			}
			if err := os.RemoveAll(r.Path); err != nil {
				return err
			}
		}
		items = append(items, manageQuarantine{OperationID: manageID("op"), Service: service, Original: r.Path, Quarantine: q, SHA256: manageHash(q), QuarantinedAt: time.Now().UTC().Format(time.RFC3339Nano), PurgeAfter: time.Now().Add(grace).UTC().Format(time.RFC3339Nano)})
		newCount++
	}
	if err := manageWriteQuarantine(service, s, items); err != nil {
		return err
	}
	if full {
		if err := managePurgeDue(service, s); err != nil {
			return err
		}
	}
	return manageOutput(map[string]any{"service": service, "status": "ok", "quarantined": newCount, "full": full})
}
func manageQuarantinePath(service string, s scope) string {
	return filepath.Join(manageStateDir(s), service, "quarantine.json")
}
func manageReadQuarantine(service string, s scope) ([]manageQuarantine, error) {
	b, e := os.ReadFile(manageQuarantinePath(service, s))
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var q []manageQuarantine
	e = json.Unmarshal(b, &q)
	return q, e
}
func manageWriteQuarantine(service string, s scope, q []manageQuarantine) error {
	if err := os.MkdirAll(filepath.Dir(manageQuarantinePath(service, s)), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(q, "", "  ")
	return os.WriteFile(manageQuarantinePath(service, s), b, 0o600)
}
func managePurgeDue(service string, s scope) error {
	q, e := manageReadQuarantine(service, s)
	if e != nil {
		return e
	}
	now := time.Now()
	out := q
	for i := range q {
		if q[i].Restored {
			continue
		}
		d, e := time.Parse(time.RFC3339Nano, q[i].PurgeAfter)
		if e == nil && now.After(d) && manageQuarantineProtected(q[i], service, s) {
			if e = os.RemoveAll(q[i].Quarantine); e != nil {
				return e
			}
			out[i].Restored = true
		}
	}
	return manageWriteQuarantine(service, s, out)
}
func manageQuarantineProtected(q manageQuarantine, service string, s scope) bool {
	root := filepath.Join(manageStateDir(s), service, "snapshots")
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		b, err := os.ReadFile(filepath.Join(root, entry.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var meta manageSnapshotMeta
		if json.Unmarshal(b, &meta) != nil || !meta.Complete || meta.State != "verified" {
			continue
		}
		if meta.Backend != "" && meta.Backend != "local" && meta.Ciphertext != "" {
			backend, err := manageBackend(meta.Backend)
			if err != nil || backendVerify(backend, meta.Ciphertext, meta.CiphertextSHA256) != nil {
				continue
			}
		}
		for _, captured := range meta.Resources {
			if captured.Path == q.Original && (q.SHA256 == "" || captured.SHA256 == q.SHA256) {
				return true
			}
		}
	}
	return false
}

func manageCleanStatus(service string, s scope) error {
	q, e := manageReadQuarantine(service, s)
	if e != nil {
		return e
	}
	return manageOutput(q)
}
func manageCleanUndo(service, id string, s scope) error {
	q, e := manageReadQuarantine(service, s)
	if e != nil {
		return e
	}
	for i := range q {
		if q[i].OperationID != id {
			continue
		}
		if q[i].Restored {
			return errors.New("quarantine entry already restored")
		}
		if _, e = os.Lstat(q[i].Original); e == nil {
			return errors.New("original path now exists; refusing to overwrite")
		}
		if e = os.MkdirAll(filepath.Dir(q[i].Original), 0o755); e != nil {
			return e
		}
		if e = os.Rename(q[i].Quarantine, q[i].Original); e != nil {
			if !errors.Is(e, syscall.EXDEV) {
				return e
			}
			if e = copyManaged(q[i].Quarantine, q[i].Original); e != nil {
				return e
			}
			if e = os.RemoveAll(q[i].Quarantine); e != nil {
				return e
			}
		}
		q[i].Restored = true
		return manageWriteQuarantine(service, s, q)
	}
	return errors.New("quarantine operation not found")
}
func manageLogs(service string, s scope) error {
	p := manageJournalPath(service, s)
	f, e := os.Open(p)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	defer f.Close()
	if manageJSON {
		var out []manageOperation
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var o manageOperation
			if json.Unmarshal(sc.Bytes(), &o) == nil {
				out = append(out, o)
			}
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return sc.Err()
	}
	_, e = io.Copy(os.Stdout, f)
	return e
}
