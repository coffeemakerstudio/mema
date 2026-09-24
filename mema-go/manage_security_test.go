package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestManageUnsupportedSnapshotFormatFailsClosed(t *testing.T) {
	dir := t.TempDir()
	meta := manageSnapshotMeta{SnapshotFormat: 99, ID: "snap-invalid", Service: "fixture", Complete: true, State: "verified"}
	b, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := readSnapshotMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, cleanup, err := manageEnsureSnapshotPayload(loaded, dir); err == nil {
		cleanup()
		t.Fatal("unsupported snapshot format was accepted")
	}
}

func TestManageRestoreRejectsSnapshotPathTraversal(t *testing.T) {
	if err := validatePathComponent("../outside", "snapshot"); err == nil {
		t.Fatal("snapshot path traversal was accepted")
	}
}
