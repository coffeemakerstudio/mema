package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	if meta.SnapshotFormat != manageSnapshotFormatVersion {
		return fmt.Errorf("unsupported snapshot format %d", meta.SnapshotFormat)
	}
	if args[0] == "inspect" {
		return manageOutput(meta)
	}
	root, cleanup, err := manageEnsureSnapshotPayload(meta, dir)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := verifySnapshotPayload(meta, root); err != nil {
		return err
	}
	if args[0] == "verify" {
		return manageOutput(map[string]any{"snapshot": meta.ID, "service": meta.Service, "status": "verified", "encryption": meta.Encryption, "backend": meta.Backend})
	}
	return recoverRestore(meta, root)
}
func readSnapshotMeta(dir string) (manageSnapshotMeta, error) {
	var meta manageSnapshotMeta
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return meta, fmt.Errorf("read snapshot metadata: %w", err)
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		return meta, fmt.Errorf("parse snapshot metadata: %w", err)
	}
	return meta, nil
}
func recoverRestore(meta manageSnapshotMeta, root string) error {
	manifestBytes, err := os.ReadFile(filepath.Join(filepath.Dir(root), "service-manifest.json"))
	if err != nil {
		// Encrypted payloads are extracted beside filesystem/. Plain snapshots
		// keep the embedded manifest beside the snapshot directory.
		return errors.New("snapshot does not contain an embedded service manifest")
	}
	var manifest manageManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return err
	}
	if manifest.Service != meta.Service || manifest.Version != manageManifestVersion {
		return errors.New("embedded service manifest is incompatible")
	}
	if err := validateManageManifest(manifest); err != nil {
		return err
	}
	if err := validateSnapshotResources(meta, manifest); err != nil {
		return err
	}
	if err := manageApplyTargets(manifest, "stop"); err != nil {
		return err
	}
	for _, resource := range meta.Resources {
		if err := manageRestoreOne(filepath.Join(root, filepath.FromSlash(resource.SnapshotPath)), resource.Path, resource); err != nil {
			return err
		}
	}
	if err := manageApplyTargets(manifest, "activate"); err != nil {
		return err
	}
	return manageOutput(map[string]any{"snapshot": meta.ID, "service": meta.Service, "status": "restored", "completed_at": time.Now().UTC().Format(time.RFC3339Nano)})
}
