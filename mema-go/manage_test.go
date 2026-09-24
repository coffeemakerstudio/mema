package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestManageSnapshotRestoreAndCleanSafety(t *testing.T) {
	root := t.TempDir()
	manifestDir := filepath.Join(root, "manifests")
	stateDir := filepath.Join(root, "state")
	dataPath := filepath.Join(root, "service", "data.txt")
	cleanupPath := filepath.Join(root, "service", "old.txt")
	if err := os.MkdirAll(filepath.Dir(dataPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataPath, []byte("known-good"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cleanupPath, []byte("old-state"), 0o640); err != nil {
		t.Fatal(err)
	}
	manifest := manageManifest{
		Version: 1, Service: "fixture",
		Data:    []manageResource{{Path: dataPath}},
		Cleanup: []manageResource{{Path: cleanupPath, Clean: "quarantine"}},
		Clean:   manageCleanPolicy{GracePeriod: "7d"},
	}
	b, _ := json.Marshal(manifest)
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "fixture.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMA_MANAGE_MANIFEST_DIR", manifestDir)
	t.Setenv("MEMA_MANAGE_STATE_DIR", stateDir)
	oldJSON := manageJSON
	manageJSON = true
	defer func() { manageJSON = oldJSON }()
	s := localScope()
	m, path, err := loadManageManifest("fixture", s)
	if err != nil {
		t.Fatal(err)
	}
	if err := manageSnapshot(m, path, "fixture", s); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(stateDir, "fixture", "snapshots"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("snapshot entries = %d, err = %v", len(entries), err)
	}
	snapshotID := entries[0].Name()
	if err := os.Remove(dataPath); err != nil {
		t.Fatal(err)
	}
	if err := manageRestore(m, path, "fixture", snapshotID, s); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(dataPath)
	if err != nil || string(content) != "known-good" {
		t.Fatalf("restored content = %q, err = %v", content, err)
	}
	if err := manageClean(m, path, "fixture", s, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cleanupPath); !os.IsNotExist(err) {
		t.Fatalf("cleanup source still exists or unexpected error: %v", err)
	}
	q, err := manageReadQuarantine("fixture", s)
	if err != nil || len(q) != 1 || q[0].Restored {
		t.Fatalf("quarantine = %#v, err = %v", q, err)
	}
	if _, err := os.Stat(q[0].Quarantine); err != nil {
		t.Fatalf("fresh quarantine was purged: %v", err)
	}
	q[0].PurgeAfter = "2000-01-01T00:00:00Z"
	if err := manageWriteQuarantine("fixture", s, q); err != nil {
		t.Fatal(err)
	}
	if err := manageClean(m, path, "fixture", s, true); err != nil {
		t.Fatal(err)
	}
	q, err = manageReadQuarantine("fixture", s)
	if err != nil || !q[0].Restored {
		t.Fatalf("expired protected quarantine was not purged: %#v, err = %v", q, err)
	}
}

func TestManageEncryptedSnapshotUsesPublicKeyAndHidesPlaintext(t *testing.T) {
	root := t.TempDir()
	gpgHome := filepath.Join(root, "gpg")
	if err := os.MkdirAll(gpgHome, 0o700); err != nil {
		t.Fatal(err)
	}
	key := exec.Command("gpg", "--batch", "--homedir", gpgHome, "--passphrase", "", "--quick-generate-key", "snapshot-test <snapshot-test@example.invalid>", "default", "default", "1d")
	if output, err := key.CombinedOutput(); err != nil {
		t.Skipf("gpg unavailable for encryption qualification: %v (%s)", err, output)
	}
	fixture := filepath.Join(root, "secret.txt")
	marker := "unique-secret-snapshot-marker"
	if err := os.WriteFile(fixture, []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestDir := filepath.Join(root, "manifests")
	if err := os.MkdirAll(manifestDir, 0o700); err != nil {
		t.Fatal(err)
	}
	remoteRoot := filepath.Join(root, "remote")
	backendConfig := filepath.Join(root, "backends.json")
	backendBytes, _ := json.Marshal(map[string]manageBackendConfig{"vault": {Name: "vault", Type: "local", Root: remoteRoot}})
	if err := os.WriteFile(backendConfig, backendBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := manageManifest{Version: 1, Service: "encrypted", Data: []manageResource{{Path: fixture}}, Snapshot: manageSnapshotPolicy{Backend: "vault", Recipient: "snapshot-test@example.invalid"}}
	b, _ := json.Marshal(manifest)
	manifestPath := filepath.Join(manifestDir, "encrypted.json")
	if err := os.WriteFile(manifestPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "state")
	t.Setenv("MEMA_MANAGE_MANIFEST_DIR", manifestDir)
	t.Setenv("MEMA_MANAGE_STATE_DIR", state)
	t.Setenv("MEMA_MANAGE_GPG_HOME", gpgHome)
	t.Setenv("MEMA_MANAGE_BACKENDS_FILE", backendConfig)
	m, _, err := loadManageManifest("encrypted", localScope())
	if err != nil {
		t.Fatal(err)
	}
	if err := manageSnapshot(m, manifestPath, "encrypted", localScope()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(state, "encrypted", "snapshots"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("snapshot entries = %d, err = %v", len(entries), err)
	}
	dir := filepath.Join(state, "encrypted", "snapshots", entries[0].Name())
	meta, err := readSnapshotMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Encryption != "gpg-public-key" || meta.State != "verified" {
		t.Fatalf("unexpected encryption state: %#v", meta)
	}
	if _, err := os.Stat(filepath.Join(dir, "filesystem")); !os.IsNotExist(err) {
		t.Fatalf("plaintext filesystem remains: %v", err)
	}
	remoteObject := filepath.Join(remoteRoot, meta.Ciphertext)
	if _, err := os.Stat(remoteObject); err != nil {
		t.Fatalf("remote encrypted object missing: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "payload.gpg")); err != nil {
		t.Fatal(err)
	}
	ciphertext, err := os.ReadFile(remoteObject)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ciphertext), marker) {
		t.Fatal("plaintext fixture marker found in encrypted payload")
	}
	rootPath, cleanup, err := manageEnsureSnapshotPayload(meta, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := verifySnapshotPayload(meta, rootPath); err != nil {
		t.Fatal(err)
	}
}

func TestManageLockRecoversDeadOwner(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MEMA_MANAGE_STATE_DIR", filepath.Join(root, "state"))
	s := localScope()
	if err := os.MkdirAll(manageStateDir(s), 0o755); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(manageStateDir(s), "fixture.lock")
	if err := os.WriteFile(lockPath, []byte("999999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, err := manageLock("fixture", s)
	if err != nil {
		t.Fatalf("stale lock was not recovered: %v", err)
	}
	defer unlock()
	if err := os.WriteFile(lockPath, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manageLock("fixture", s); err == nil {
		t.Fatal("live lock was not rejected")
	}
}

func TestManageCleanUndoRefusesConflict(t *testing.T) {
	root := t.TempDir()
	s := localScope()
	t.Setenv("MEMA_MANAGE_STATE_DIR", filepath.Join(root, "state"))
	service := "fixture"
	original := filepath.Join(root, "original")
	quarantine := filepath.Join(root, "quarantine")
	if err := os.WriteFile(quarantine, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	q := []manageQuarantine{{OperationID: "op-1", Service: service, Original: original, Quarantine: quarantine}}
	if err := manageWriteQuarantine(service, s, q); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manageCleanUndo(service, "op-1", s); err == nil {
		t.Fatal("clean undo overwrote conflicting live data")
	}
	content, _ := os.ReadFile(original)
	if string(content) != "new" {
		t.Fatalf("live data changed to %q", content)
	}
}
