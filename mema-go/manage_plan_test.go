package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureManagePlanOutput(t *testing.T, fn func() error) string {
	t.Helper()
	oldJSON := manageJSON
	manageJSON = true
	defer func() { manageJSON = oldJSON }()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = write
	callErr := fn()
	_ = write.Close()
	os.Stdout = old
	output, readErr := io.ReadAll(read)
	_ = read.Close()
	if callErr != nil {
		t.Fatal(callErr)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(output)
}

func TestManageSnapshotDryRunIsReadOnlyAndRedactsSecrets(t *testing.T) {
	root := t.TempDir()
	manifestDir := filepath.Join(root, "manifests")
	stateDir := filepath.Join(root, "state")
	resource := filepath.Join(root, "service", "binary")
	secret := "unique-dry-run-secret-marker"
	if err := os.MkdirAll(filepath.Dir(resource), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resource, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := manageManifest{
		Version: 1, Service: "fixture",
		Binary: []manageResource{{Path: resource, Role: "binary"}},
		Env:    []manageResource{{Path: filepath.Join(root, "service.env"), Role: "env", Secret: true}},
		Variables: map[string]manageVariable{
			"TOKEN": {Type: "secret-ref", Default: secret},
		},
	}
	if err := os.WriteFile(filepath.Join(root, "service.env"), []byte("TOKEN="+secret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(manifestDir, "fixture.json")
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMA_MANAGE_STATE_DIR", stateDir)
	t.Setenv("MEMA_MANAGE_MANIFEST_DIR", manifestDir)
	before := sha256.Sum256([]byte("binary"))
	output := captureManagePlanOutput(t, func() error {
		return managePlanSnapshot(manifest, manifestPath, "fixture", localScope(), nil, manageInvocationOptions{dryRun: true, print: true})
	})
	var plan managePlan
	if err := json.Unmarshal([]byte(output), &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.DryRun || plan.MutationsPerformed || plan.Operation != "snapshot" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if strings.Contains(output, secret) {
		t.Fatal("secret marker appeared in dry-run output")
	}
	if !strings.Contains(output, resource) {
		t.Fatal("declared resource missing from dry-run output")
	}
	if got, err := os.ReadFile(resource); err != nil || hex.EncodeToString(before[:]) != hex.EncodeToString(sha256Bytes(got)) {
		t.Fatalf("resource changed during dry-run: %v", err)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created state: %v", err)
	}
}

func TestManageRestoreAndCleanDryRunDoNotMutate(t *testing.T) {
	root := t.TempDir()
	manifestDir := filepath.Join(root, "manifests")
	stateDir := filepath.Join(root, "state")
	resource := filepath.Join(root, "service", "data")
	cleanup := filepath.Join(root, "service", "old")
	if err := os.MkdirAll(filepath.Dir(resource), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]string{resource: "data", cleanup: "old"} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := manageManifest{Version: 1, Service: "fixture", Data: []manageResource{{Path: resource}}, Cleanup: []manageResource{{Path: cleanup, Clean: "quarantine"}}}
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(manifestDir, "fixture.json")
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMA_MANAGE_STATE_DIR", stateDir)
	meta := manageSnapshotMeta{SnapshotFormat: 1, ID: "snap-test", ManifestVersion: 1, Service: "fixture", State: "verified", Complete: true, Resources: []manageCaptured{{Path: resource, SnapshotPath: "service/data", Kind: "file"}}}
	snapshotDir := filepath.Join(stateDir, "fixture", "snapshots", meta.ID)
	if err := os.MkdirAll(snapshotDir, 0o700); err != nil {
		t.Fatal(err)
	}
	metaBytes, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(snapshotDir, "manifest.json"), metaBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(cleanup)
	restoreOutput := captureManagePlanOutput(t, func() error {
		return managePlanRestore(manifest, manifestPath, "fixture", meta.ID, localScope(), nil, manageInvocationOptions{dryRun: true, print: true})
	})
	if !strings.Contains(restoreOutput, resource) || !strings.Contains(restoreOutput, "conflict") {
		t.Fatalf("restore plan lacks destination/conflict: %s", restoreOutput)
	}
	cleanOutput := captureManagePlanOutput(t, func() error {
		return managePlanClean(manifest, manifestPath, "fixture", localScope(), false, nil, manageInvocationOptions{dryRun: true, print: true})
	})
	if !strings.Contains(cleanOutput, cleanup) {
		t.Fatalf("clean plan lacks candidate: %s", cleanOutput)
	}
	fullCleanOutput := captureManagePlanOutput(t, func() error {
		return managePlanClean(manifest, manifestPath, "fixture", localScope(), true, nil, manageInvocationOptions{dryRun: true, print: true})
	})
	if !strings.Contains(fullCleanOutput, "purge_candidates") {
		t.Fatalf("full clean plan lacks purge candidates: %s", fullCleanOutput)
	}
	if got, _ := os.ReadFile(cleanup); string(got) != string(before) {
		t.Fatal("clean dry-run changed cleanup resource")
	}
	if _, err := os.Stat(filepath.Join(stateDir, "fixture", "quarantine.json")); !os.IsNotExist(err) {
		t.Fatalf("clean dry-run wrote quarantine state: %v", err)
	}
}

func sha256Bytes(data []byte) []byte {
	hash := sha256.Sum256(data)
	return hash[:]
}
