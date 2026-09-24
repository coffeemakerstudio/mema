package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestManageConfigurationPrecedenceAndProvenance(t *testing.T) {
	root := t.TempDir()
	manifestDir := filepath.Join(root, "manifests")
	if err := os.MkdirAll(manifestDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := manageManifest{
		Version: 1, Service: "layered",
		Variables: map[string]manageVariable{
			"BACKUP_BACKEND":   {Type: "backend-ref", Default: "local", Overridable: true, Source: manageVariableSource{Env: "TEST_BACKEND"}},
			"BACKUP_FILE":      {Type: "string", Default: "${SERVICE}-${SNAPSHOT_ID}.mema", Overridable: true},
			"CLEAN_GRACE":      {Type: "duration", Default: "7d", Overridable: true},
			"BACKUP_RECIPIENT": {Type: "secret-ref", Source: manageVariableSource{Env: "TEST_BACKUP_RECIPIENT"}},
		},
		Backup: manageBackupPolicy{Backend: "${BACKUP_BACKEND}", File: "${BACKUP_FILE}", Encryption: manageEncryptionPolicy{Type: "gpg", Recipient: "${BACKUP_RECIPIENT}"}},
	}
	b, _ := json.Marshal(manifest)
	manifestPath := filepath.Join(manifestDir, "layered.json")
	if err := os.WriteFile(manifestPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	serviceConfig := map[string]any{"variables": map[string]string{"BACKUP_BACKEND": "service", "CLEAN_GRACE": "10d"}}
	sb, _ := json.Marshal(serviceConfig)
	if err := os.WriteFile(filepath.Join(manifestDir, "layered.local.json"), sb, 0o600); err != nil {
		t.Fatal(err)
	}
	backendConfig := filepath.Join(root, "backends.json")
	backendBytes, _ := json.Marshal(map[string]manageBackendConfig{"host": {Name: "host", Type: "local", Root: filepath.Join(root, "host-store")}, "service": {Name: "service", Type: "local", Root: filepath.Join(root, "service-store")}})
	if err := os.WriteFile(backendConfig, backendBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMA_MANAGE_BACKENDS_FILE", backendConfig)
	hostConfig := map[string]any{"variables": map[string]string{"BACKUP_BACKEND": "host", "CLEAN_GRACE": "14d"}}
	hb, _ := json.Marshal(hostConfig)
	hostPath := filepath.Join(root, "host.json")
	if err := os.WriteFile(hostPath, hb, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMA_MANAGE_MANIFEST_DIR", manifestDir)
	t.Setenv("MEMA_MANAGE_HOST_CONFIG", hostPath)
	t.Setenv("TEST_BACKEND", "service")
	t.Setenv("TEST_BACKUP_RECIPIENT", "public-recipient")
	m, _, err := loadManageManifest("layered", localScope())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := resolveManageConfig(m, manifestPath, "layered", localScope(), map[string]string{"BACKUP_BACKEND": "local", "BACKUP_FILE": "manual-before-upgrade.mema"}, map[string]string{"SERVICE": "layered", "SNAPSHOT_ID": "snap-test", "HOST": "host"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Values["BACKUP_BACKEND"].Value != "local" || cfg.Values["BACKUP_BACKEND"].Source != "invocation" {
		t.Fatalf("backend = %#v", cfg.Values["BACKUP_BACKEND"])
	}
	if cfg.Values["BACKUP_FILE"].Value != "manual-before-upgrade.mema" {
		t.Fatalf("file = %#v", cfg.Values["BACKUP_FILE"])
	}
	if cfg.CleanGrace != 14*24*60*60*1000000000 {
		t.Fatalf("clean grace = %s", cfg.CleanGrace)
	}
	if cfg.Values["BACKUP_RECIPIENT"].Value != "public-recipient" || !cfg.Values["BACKUP_RECIPIENT"].Secret {
		t.Fatalf("recipient was not resolved as secret: %#v", cfg.Values["BACKUP_RECIPIENT"])
	}
	if cfg.Backup.Encryption.Recipient != "public-recipient" {
		t.Fatalf("backup recipient = %q", cfg.Backup.Encryption.Recipient)
	}
	cfg, err = resolveManageConfig(m, manifestPath, "layered", localScope(), nil, map[string]string{"SERVICE": "layered", "SNAPSHOT_ID": "snap-next", "HOST": "host"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Backup.Backend != "service" || cfg.Backup.File != "layered-snap-next.mema" || cfg.CleanGrace != 14*24*60*60*1000000000 {
		t.Fatalf("persistent layering = %#v grace=%s", cfg.Backup, cfg.CleanGrace)
	}
}

func TestManageConfigurationRejectsUnsafeAndFixedOverrides(t *testing.T) {
	m := manageManifest{Version: 1, Service: "fixture", Variables: map[string]manageVariable{
		"FIXED": {Type: "string", Default: "x"},
		"SAFE":  {Type: "string", Default: "x", Overridable: true},
	}}
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.json")
	b, _ := json.Marshal(m)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveManageConfig(m, path, "fixture", localScope(), map[string]string{"UNKNOWN": "x"}, map[string]string{"SERVICE": "fixture", "SNAPSHOT_ID": "s"}); err == nil {
		t.Fatal("unknown override accepted")
	}
	if _, err := resolveManageConfig(m, path, "fixture", localScope(), map[string]string{"FIXED": "x"}, map[string]string{"SERVICE": "fixture", "SNAPSHOT_ID": "s"}); err == nil {
		t.Fatal("fixed override accepted")
	}
	m.Backup.File = "${SAFE}"
	if _, err := resolveManageConfig(m, path, "fixture", localScope(), map[string]string{"SAFE": "../../etc/passwd"}, map[string]string{"SERVICE": "fixture", "SNAPSHOT_ID": "s"}); err == nil {
		t.Fatal("unsafe backup filename accepted")
	}
}

func TestManageInvocationSetParsing(t *testing.T) {
	overrides, args, err := parseManageInvocation([]string{"service", "snapshot", "--set", "BACKUP_BACKEND=local", "--set=BACKUP_FILE=manual.mema"})
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 2 || overrides["BACKUP_BACKEND"] != "local" || overrides["BACKUP_FILE"] != "manual.mema" {
		t.Fatalf("parsed overrides=%#v args=%#v", overrides, args)
	}
	overrides, args, options, err := parseManageInvocationOptions([]string{"service", "snapshot", "--dry-run", "--print", "--set=NAME=value"})
	if err != nil || len(args) != 2 || overrides["NAME"] != "value" || !options.dryRun || !options.print {
		t.Fatalf("parsed options=%#v overrides=%#v args=%#v err=%v", options, overrides, args, err)
	}
}
