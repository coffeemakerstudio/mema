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

func TestManageRestoreRejectsMergedUsrAndArbitrarySymlinkParents(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "usr", "lib", "systemd", "system"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("usr", filepath.Join(root, "lib")); err != nil {
		t.Fatal(err)
	}
	for _, destination := range []string{
		filepath.Join(root, "lib", "systemd", "system", "tparun.service"),
		filepath.Join(root, "usr", "lib", "systemd", "system", "tparun.service"),
	} {
		err := validateRestoreDestinations([]manageCaptured{{Path: destination}})
		if destination == filepath.Join(root, "lib", "systemd", "system", "tparun.service") && err == nil {
			t.Fatalf("merged-/usr symlink-parent destination %q was accepted", destination)
		}
		if destination == filepath.Join(root, "usr", "lib", "systemd", "system", "tparun.service") && err != nil {
			t.Fatalf("canonical non-symlink destination %q was rejected: %v", destination, err)
		}
	}
	link := filepath.Join(root, "arbitrary-link")
	if err := os.Symlink("usr", link); err != nil {
		t.Fatal(err)
	}
	if err := validateRestoreDestinations([]manageCaptured{{Path: filepath.Join(link, "lib", "service.conf")}}); err == nil {
		t.Fatal("arbitrary symlink-parent destination was accepted")
	}
}
