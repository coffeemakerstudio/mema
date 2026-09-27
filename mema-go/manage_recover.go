package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func recoverCommand(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: mema recover <inspect|verify|restore> <snapshot-directory>")
	}
	if args[0] != "inspect" && args[0] != "verify" && args[0] != "restore" {
		return fmt.Errorf("unknown recover operation %q", args[0])
	}
	dir, err := filepath.Abs(args[1])
	if err != nil {
		return err
	}
	meta, err := readSnapshotMeta(dir)
	if err != nil {
		return err
	}
	if meta.SnapshotFormat != 1 && meta.SnapshotFormat != manageSnapshotFormatVersion {
		return fmt.Errorf("unsupported snapshot format %d", meta.SnapshotFormat)
	}
	if args[0] == "inspect" {
		return manageOutput(meta)
	}
	if meta.SnapshotFormat == manageSnapshotFormatVersion {
		if err := verifySnapshotDirectoryV2(dir, meta, false); err != nil {
			return err
		}
	}
	root, cleanup, err := recoverRootFromSnapshot(meta, dir)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := verifySnapshotPayload(meta, root); err != nil {
		return err
	}
	manifest, err := readEmbeddedManifest(root, meta)
	if err != nil {
		return err
	}
	if err := validateSnapshotResources(meta, manifest); err != nil {
		return err
	}
	if args[0] == "verify" {
		return manageOutput(map[string]any{"snapshot": meta.ID, "service": meta.Service, "status": "verified", "encryption": meta.Encryption, "backend": meta.Backend})
	}
	return recoverRestore(meta, root)
}
func readSnapshotMeta(dir string) (manageSnapshotMeta, error) {
	var meta manageSnapshotMeta
	path := filepath.Join(dir, "manifest.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return meta, fmt.Errorf("read snapshot metadata: %w", err)
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		return meta, fmt.Errorf("parse snapshot metadata: %w", err)
	}
	digestPath := filepath.Join(dir, "manifest.sha256")
	digestBytes, digestErr := os.ReadFile(digestPath)
	if meta.SnapshotFormat == manageSnapshotFormatVersion && digestErr != nil {
		return meta, errors.New("snapshot manifest integrity sidecar is missing")
	}
	if digestErr == nil {
		fields := strings.Fields(string(digestBytes))
		if len(fields) != 2 || fields[1] != "manifest.json" || len(fields[0]) != 64 {
			return meta, errors.New("snapshot manifest integrity sidecar is invalid")
		}
		actual := snapshotSHA256(b)
		if actual != fields[0] {
			return meta, errors.New("snapshot manifest checksum mismatch")
		}
	} else if !os.IsNotExist(digestErr) {
		return meta, fmt.Errorf("read snapshot manifest integrity sidecar: %w", digestErr)
	}
	return meta, nil
}
func recoverRestore(meta manageSnapshotMeta, root string) error {
	manifest, err := readEmbeddedManifest(root, meta)
	if err != nil {
		return err
	}
	if err := validateManageManifest(manifest); err != nil {
		return err
	}
	if err := validateSnapshotResources(meta, manifest); err != nil {
		return err
	}
	if err := manageApplyRestoreTransaction(meta, root, manifest); err != nil {
		return err
	}
	return manageOutput(map[string]any{"snapshot": meta.ID, "service": meta.Service, "status": "restored", "completed_at": time.Now().UTC().Format(time.RFC3339Nano)})
}
