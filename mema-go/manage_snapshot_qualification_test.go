package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type recoveryFixture struct {
	t            *testing.T
	root         string
	data         string
	manifestDir  string
	manifestPath string
	state        string
	remote       string
	service      string
	server       *httptest.Server
	failRev      string
}

func newRecoveryFixture(t *testing.T, service string) *recoveryFixture {
	t.Helper()
	root, err := os.MkdirTemp("", "mema-dr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	f := &recoveryFixture{t: t, root: root, data: filepath.Join(root, "application", "data"), manifestDir: filepath.Join(root, "manifests"), state: filepath.Join(root, "mema-state"), remote: filepath.Join(root, "remote"), service: service}
	if err := os.MkdirAll(f.manifestDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		var state struct {
			Revision string `json:"revision"`
			Count    int    `json:"count"`
		}
		b, err := os.ReadFile(filepath.Join(f.data, "nested", "application.json"))
		if err != nil || json.Unmarshal(b, &state) != nil || state.Revision == "" {
			http.Error(w, "application state unavailable", http.StatusServiceUnavailable)
			return
		}
		if state.Revision == f.failRev {
			http.Error(w, "injected unhealthy revision", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ready":true}`)
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		var state map[string]any
		b, err := os.ReadFile(filepath.Join(f.data, "nested", "application.json"))
		if err != nil || json.Unmarshal(b, &state) != nil {
			http.Error(w, "application state unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state)
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	t.Setenv("MEMA_MANAGE_MANIFEST_DIR", f.manifestDir)
	t.Setenv("MEMA_MANAGE_STATE_DIR", f.state)
	backends := map[string]manageBackendConfig{"vault": {Name: "vault", Type: "local", Root: f.remote}}
	data, _ := json.Marshal(backends)
	backendsPath := filepath.Join(root, "backends.json")
	if err := os.WriteFile(backendsPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMA_MANAGE_BACKENDS_FILE", backendsPath)
	if err := f.populate("A", 41); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *recoveryFixture) populate(revision string, count int) error {
	if err := os.RemoveAll(f.data); err != nil {
		return err
	}
	for _, dir := range []string{filepath.Join(f.data, "nested"), filepath.Join(f.data, "bin"), filepath.Join(f.data, "empty-dir")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	state, _ := json.Marshal(map[string]any{"revision": revision, "count": count, "records": []string{"alpha", "beta", "gamma"}})
	if err := os.WriteFile(filepath.Join(f.data, "nested", "application.json"), append(state, '\n'), 0o640); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(f.data, "nested", "value.txt"), []byte("persistent payload\n"), 0o640); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(f.data, "empty"), nil, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(f.data, "bin", "run"), []byte("#!/bin/sh\nexit 0\n"), 0o751); err != nil {
		return err
	}
	if err := os.Symlink("nested/value.txt", filepath.Join(f.data, "current")); err != nil {
		return err
	}
	socketPath := filepath.Join(f.data, "runtime.sock")
	socket, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	if unix, ok := socket.(*net.UnixListener); ok {
		unix.SetUnlinkOnClose(false)
	}
	if err := socket.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(f.data, "service.pid"), []byte("1234\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(f.data, "scratch.tmp"), []byte("ephemeral\n"), 0o600); err != nil {
		return err
	}
	return nil
}

func (f *recoveryFixture) manifest(encrypted bool) manageManifest {
	m := manageManifest{
		Version: 1, Service: f.service,
		Data:   []manageResource{{Path: f.data, Role: "persistent-data", Exclude: []string{"service.pid", "scratch.tmp", "runtime.sock"}}},
		Health: manageHealth{Ready: f.server.URL + "/ready", Version: f.server.URL + "/version"},
		Variables: map[string]manageVariable{
			"BACKUP_BACKEND": {Type: "backend-ref", Default: "local"},
			"DR_RECIPIENT":   {Type: "secret-ref", Source: manageVariableSource{Env: "MEMA_TEST_DR_RECIPIENT"}},
		},
		Backup: manageBackupPolicy{Format: "mema-snapshot-v2", Backend: "${BACKUP_BACKEND}", File: "${SERVICE}-${SNAPSHOT_ID}.mema"},
	}
	if encrypted {
		m.Variables["BACKUP_BACKEND"] = manageVariable{Type: "backend-ref", Default: "vault"}
		m.Backup.Encryption = manageEncryptionPolicy{Type: "gpg", Recipient: "${DR_RECIPIENT}"}
	}
	return m
}

func (f *recoveryFixture) writeManifest(m manageManifest) {
	f.t.Helper()
	f.manifestPath = filepath.Join(f.manifestDir, f.service+".json")
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(f.manifestPath, b, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *recoveryFixture) snapshotEntries() []manageSnapshotMeta {
	f.t.Helper()
	root := filepath.Join(f.state, f.service, "snapshots")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		f.t.Fatal(err)
	}
	var out []manageSnapshotMeta
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".partial-") {
			continue
		}
		meta, err := readSnapshotMeta(filepath.Join(root, entry.Name()))
		if err == nil {
			out = append(out, meta)
		}
	}
	return out
}

func (f *recoveryFixture) call(fn func() error) error {
	_, err := f.callOutput(fn)
	return err
}

func (f *recoveryFixture) callOutput(fn func() error) (string, error) {
	f.t.Helper()
	old := manageJSON
	manageJSON = true
	defer func() { manageJSON = old }()
	return f.captureOutput(fn)
}

func (f *recoveryFixture) captureOutput(fn func() error) (string, error) {
	f.t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		f.t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = write
	callErr := fn()
	_ = write.Close()
	os.Stdout = old
	output, readErr := io.ReadAll(read)
	_ = read.Close()
	if readErr != nil {
		return string(output), readErr
	}
	return string(output), callErr
}

func (f *recoveryFixture) assertHealthy() {
	f.t.Helper()
	if err := verifyManageHealth(manageHealth{Ready: f.server.URL + "/ready", Version: f.server.URL + "/version"}); err != nil {
		f.t.Fatalf("fixture service health failed: %v", err)
	}
}

func makeGPGWriterAndRecoveryHomes(t *testing.T) (string, string, string) {
	t.Helper()
	if _, err := exec.LookPath("gpg"); err != nil {
		t.Fatal("gpg is required for encrypted disaster-recovery qualification")
	}
	root := t.TempDir()
	recoveryHome := filepath.Join(root, "recovery-gpg")
	writerHome := filepath.Join(root, "writer-gpg-public-only")
	for _, home := range []string{recoveryHome, writerHome} {
		if err := os.Mkdir(home, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	recipient := "fixture-dr@example.invalid"
	cmd := exec.Command("gpg", "--batch", "--homedir", recoveryHome, "--passphrase", "", "--quick-generate-key", recipient, "default", "default", "1d")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate isolated recovery key: %v: %s", err, output)
	}
	pub := exec.Command("gpg", "--batch", "--homedir", recoveryHome, "--export", recipient)
	publicKey, err := pub.Output()
	if err != nil {
		t.Fatal(err)
	}
	imp := exec.Command("gpg", "--batch", "--homedir", writerHome, "--import")
	imp.Stdin = bytes.NewReader(publicKey)
	if output, err := imp.CombinedOutput(); err != nil {
		t.Fatalf("import public recovery key: %v: %s", err, output)
	}
	secrets := exec.Command("gpg", "--batch", "--homedir", writerHome, "--with-colons", "--list-secret-keys")
	secretOutput, err := secrets.Output()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(secretOutput, []byte("sec:")) || bytes.Contains(secretOutput, []byte("ssb:")) {
		t.Fatal("backup writer unexpectedly has private GPG material")
	}
	return writerHome, recoveryHome, recipient
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "runtime.sock" || rel == "service.pid" || rel == "scratch.tmp" {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		kind := "file"
		data := ""
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			kind, data = "symlink", "->"+mustReadlink(t, path)
		case info.IsDir():
			kind = "directory"
		case info.Mode().IsRegular():
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			data = snapshotSHA256(content)
		default:
			kind = "special"
		}
		uid, gid := -1, -1
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(stat.Uid), int(stat.Gid)
		}
		out[filepath.ToSlash(rel)] = fmt.Sprintf("%s|%04o|%d:%d|%d|%s", kind, info.Mode().Perm(), uid, gid, info.Size(), data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mustReadlink(t *testing.T, path string) string {
	t.Helper()
	value, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func assertSemanticState(t *testing.T, data string, revision string, count int) {
	t.Helper()
	var got struct {
		Revision string   `json:"revision"`
		Count    int      `json:"count"`
		Records  []string `json:"records"`
	}
	b, err := os.ReadFile(filepath.Join(data, "nested", "application.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Revision != revision || got.Count != count || strings.Join(got.Records, ",") != "alpha,beta,gamma" {
		t.Fatalf("semantic application state = %#v", got)
	}
}

func TestManageBackupSameHostFreshHostAndEncryptedDRRoundTrip(t *testing.T) {
	f := newRecoveryFixture(t, "recovery-roundtrip")
	writerHome, recoveryHome, recipient := makeGPGWriterAndRecoveryHomes(t)
	t.Setenv("MEMA_TEST_DR_RECIPIENT", recipient)
	t.Setenv("MEMA_MANAGE_GPG_HOME", writerHome)
	localManifest := f.manifest(false)
	f.writeManifest(localManifest)
	wantTree := snapshotTree(t, f.data)
	if err := f.call(func() error { return manageSnapshot(localManifest, f.manifestPath, f.service, localScope()) }); err != nil {
		t.Fatalf("snapshot creation: %v", err)
	}
	entries := f.snapshotEntries()
	if len(entries) != 1 || entries[0].SnapshotFormat != 2 || !entries[0].Complete || entries[0].State != "verified" {
		t.Fatalf("snapshot metadata is not complete/identified: %#v", entries)
	}
	localSnapshot := entries[0]
	if localSnapshot.ID == "" || localSnapshot.CreatedAt == "" || localSnapshot.Host == "" || localSnapshot.ServiceManifestSHA256 == "" || localSnapshot.PayloadSHA256 == "" {
		t.Fatalf("snapshot identity/metadata fields missing: %#v", localSnapshot)
	}
	if len(localSnapshot.Excluded) != 3 {
		t.Fatalf("intentional exclusions not recorded: %#v", localSnapshot.Excluded)
	}
	if err := f.call(func() error { return manageVerifySnapshot(f.service, localSnapshot.ID, localScope()) }); err != nil {
		t.Fatalf("local snapshot verification: %v", err)
	}
	localDir := filepath.Join(f.state, f.service, "snapshots", localSnapshot.ID)
	if _, err := os.Stat(filepath.Join(localDir, "manifest.sha256")); err != nil {
		t.Fatalf("manifest integrity sidecar missing: %v", err)
	}
	if err := f.call(func() error { return recoverCommand([]string{"verify", localDir}) }); err != nil {
		t.Fatalf("standalone local recovery verification: %v", err)
	}

	remoteManifest := f.manifest(true)
	f.writeManifest(remoteManifest)
	if err := f.call(func() error {
		return manageBackupWithOverrides(remoteManifest, f.manifestPath, f.service, localScope(), nil)
	}); err != nil {
		t.Fatalf("encrypted backup creation: %v", err)
	}
	entries = f.snapshotEntries()
	if len(entries) != 2 {
		t.Fatalf("expected local snapshot plus encrypted backup, got %d", len(entries))
	}
	var backup manageSnapshotMeta
	for _, entry := range entries {
		if entry.Encryption == "gpg-public-key" {
			backup = entry
		}
	}
	if backup.ID == "" || backup.EncryptionRecipient != "<configured>" || backup.PayloadSHA256 == "" || backup.BackendType != "local" || backup.MetadataEncryption != "gpg-public-key" {
		t.Fatalf("encrypted backup metadata is incomplete or leaks recipient: %#v", backup)
	}
	backupDir := filepath.Join(f.state, f.service, "snapshots", backup.ID)
	if _, err := os.Stat(filepath.Join(backupDir, "filesystem")); !os.IsNotExist(err) {
		t.Fatalf("plaintext filesystem remains in encrypted backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "payload.gpg")); err != nil {
		t.Fatalf("encrypted recovery payload missing: %v", err)
	}
	if err := f.call(func() error { return manageVerifySnapshot(f.service, backup.ID, localScope()) }); err != nil {
		t.Fatalf("remote backup/ciphertext verification with public-only writer: %v", err)
	}
	backupListOutput, err := f.callOutput(func() error { return manageBackups(f.service, localScope()) })
	if err != nil {
		t.Fatalf("list verified backups: %v", err)
	}
	var listedBackups []manageSnapshotMeta
	if err := json.Unmarshal([]byte(backupListOutput), &listedBackups); err != nil || len(listedBackups) != 1 || listedBackups[0].ID != backup.ID || listedBackups[0].State != "verified" {
		t.Fatalf("backup listing did not return only the verified backup: %s (%v)", backupListOutput, err)
	}
	if output, err := f.callOutput(func() error { return manageConfigCommand(remoteManifest, f.manifestPath, f.service, localScope(), nil) }); err != nil {
		t.Fatal(err)
	} else if strings.Contains(output, recipient) {
		t.Fatal("config JSON/text exposed encryption recipient secret")
	}

	remoteObject := filepath.Join(f.remote, filepath.FromSlash(backup.Ciphertext))
	remoteBytes, err := os.ReadFile(remoteObject)
	if err != nil {
		t.Fatal(err)
	}
	if len(remoteBytes) == 0 || bytes.Contains(remoteBytes, []byte("persistent payload")) {
		t.Fatal("remote encrypted object is empty or contains plaintext marker")
	}
	remoteMetadataObject := remoteObject + ".manifest.gpg"
	remoteMetadataBytes, err := os.ReadFile(remoteMetadataObject)
	if err != nil {
		t.Fatalf("encrypted remote metadata missing: %v", err)
	}
	if len(remoteMetadataBytes) == 0 || bytes.Contains(remoteMetadataBytes, []byte(f.data)) || bytes.Contains(remoteMetadataBytes, []byte("manifest_version")) {
		t.Fatal("remote metadata is empty or exposes plaintext snapshot metadata")
	}
	if err := validateFTPEncryptedArtifact(remoteObject); err != nil {
		t.Fatalf("payload is not integrity-protected OpenPGP ciphertext: %v", err)
	}
	if err := validateFTPEncryptedArtifact(remoteMetadataObject); err != nil {
		t.Fatalf("metadata is not integrity-protected OpenPGP ciphertext: %v", err)
	}
	for _, suffix := range []string{".manifest.json", ".manifest.sha256"} {
		if _, err := os.Stat(remoteObject + suffix); !os.IsNotExist(err) {
			t.Fatalf("legacy plaintext metadata artifact exists remotely: %s", suffix)
		}
	}

	// Read back only the remote ciphertext artifacts, decrypt the manifest, and
	// construct a fresh standalone recovery bundle from those bytes.
	remoteRecovery := filepath.Join(f.root, "fresh-recovery", "snapshot")
	if err := os.MkdirAll(remoteRecovery, 0o700); err != nil {
		t.Fatal(err)
	}
	readbackPayload := filepath.Join(f.root, "readback-payload.gpg")
	readbackMetadata := filepath.Join(f.root, "readback-metadata.gpg")
	if err := f.call(func() error {
		backend, err := manageBackend(backup.Backend)
		if err != nil {
			return err
		}
		if err := backendGet(backend, backup.Ciphertext, readbackPayload); err != nil {
			return err
		}
		return backendGet(backend, backup.Ciphertext+".manifest.gpg", readbackMetadata)
	}); err != nil {
		t.Fatalf("read back encrypted remote artifacts: %v", err)
	}
	decryptedManifest := filepath.Join(remoteRecovery, "manifest.json")
	cmd := exec.Command("gpg", "--batch", "--yes", "--homedir", recoveryHome, "--output", decryptedManifest, "--decrypt", readbackMetadata)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("decrypt read-back metadata: %v: %s", err, output)
	}
	localManifestBytes, err := os.ReadFile(filepath.Join(backupDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	decryptedManifestBytes, err := os.ReadFile(decryptedManifest)
	if err != nil || !bytes.Equal(decryptedManifestBytes, localManifestBytes) {
		t.Fatalf("read-back metadata differs from local manifest: %v", err)
	}
	readbackPayloadBytes, err := os.ReadFile(readbackPayload)
	if err != nil || !bytes.Equal(readbackPayloadBytes, remoteBytes) {
		t.Fatalf("read-back payload differs from remote object: %v", err)
	}
	if err := os.Rename(readbackPayload, filepath.Join(remoteRecovery, "payload.gpg")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remoteRecovery, "manifest.sha256"), []byte(snapshotSHA256(decryptedManifestBytes)+"  manifest.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), remoteBytes...)
	corrupt[len(corrupt)-1] ^= 0xff
	if err := os.WriteFile(remoteObject, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manageVerifySnapshot(f.service, backup.ID, localScope()); err == nil {
		t.Fatal("modified remote backup was accepted")
	}
	invalidListing, err := f.callOutput(func() error { return manageBackups(f.service, localScope()) })
	if err != nil {
		t.Fatal(err)
	}
	var invalidBackups []manageSnapshotMeta
	if err := json.Unmarshal([]byte(invalidListing), &invalidBackups); err != nil || len(invalidBackups) != 1 || invalidBackups[0].State != "invalid" || invalidBackups[0].Complete {
		t.Fatalf("corrupt remote backup was still presented as valid: %s (%v)", invalidListing, err)
	}
	if err := os.WriteFile(remoteObject, remoteBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	corruptMetadata := append([]byte(nil), remoteMetadataBytes...)
	corruptMetadata[len(corruptMetadata)-1] ^= 0xff
	if err := os.WriteFile(remoteMetadataObject, corruptMetadata, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manageVerifySnapshot(f.service, backup.ID, localScope()); err == nil {
		t.Fatal("modified remote encrypted metadata was accepted")
	}
	if err := os.WriteFile(remoteMetadataObject, remoteMetadataBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(localDir, "manifest.json")
	manifestBytes, _ := os.ReadFile(manifestPath)
	if err := os.WriteFile(manifestPath, append(manifestBytes, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manageVerifySnapshot(f.service, localSnapshot.ID, localScope()); err == nil {
		t.Fatal("modified snapshot manifest was accepted")
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	payloadPath := filepath.Join(localDir, localSnapshot.PayloadFile)
	payloadBytes, _ := os.ReadFile(payloadPath)
	if err := os.WriteFile(payloadPath, payloadBytes[:len(payloadBytes)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manageVerifySnapshot(f.service, localSnapshot.ID, localScope()); err == nil {
		t.Fatal("truncated snapshot payload was accepted")
	}
	if err := os.WriteFile(payloadPath, payloadBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	// Restore state A over a mutated same-host state B and compare full
	// included filesystem metadata, bytes, symlink, and application semantics.
	if err := f.populate("B", 900); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(f.data, "bin", "run"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertSemanticState(t, f.data, "B", 900)
	f.writeManifest(localManifest)
	if err := f.call(func() error {
		return manageRestore(localManifest, f.manifestPath, f.service, localSnapshot.ID, localScope())
	}); err != nil {
		t.Fatalf("same-host restore: %v", err)
	}
	if got := snapshotTree(t, f.data); fmt.Sprint(got) != fmt.Sprint(wantTree) {
		t.Fatalf("same-host restored tree differs\nwant=%#v\ngot=%#v", wantTree, got)
	}
	assertSemanticState(t, f.data, "A", 41)
	f.assertHealthy()
	for _, excluded := range []string{"runtime.sock", "service.pid", "scratch.tmp"} {
		if _, err := os.Lstat(filepath.Join(f.data, excluded)); !os.IsNotExist(err) {
			t.Fatalf("excluded ephemeral resource %s was restored: %v", excluded, err)
		}
	}

	// A recovery host gets the self-contained manifest and remote ciphertext,
	// not the original application tree or the writer's private key.
	freshBundle := remoteRecovery
	if err := os.RemoveAll(f.data); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(f.data), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.data); !os.IsNotExist(err) {
		t.Fatalf("fresh recovery environment unexpectedly has source data: %v", err)
	}
	t.Setenv("MEMA_MANAGE_GPG_HOME", writerHome)
	if err := recoverCommand([]string{"verify", freshBundle}); err == nil {
		t.Fatal("encrypted recovery verification succeeded without private key")
	}
	if _, err := os.Stat(f.data); !os.IsNotExist(err) {
		t.Fatal("failed decryption modified fresh recovery target")
	}
	t.Setenv("MEMA_MANAGE_GPG_HOME", recoveryHome)
	if err := f.call(func() error { return recoverCommand([]string{"verify", freshBundle}) }); err != nil {
		t.Fatalf("fresh-host encrypted verification: %v", err)
	}
	if err := f.call(func() error { return recoverCommand([]string{"restore", freshBundle}) }); err != nil {
		t.Fatalf("fresh-host restore from remote encrypted artifact: %v", err)
	}
	if got := snapshotTree(t, f.data); fmt.Sprint(got) != fmt.Sprint(wantTree) {
		t.Fatalf("fresh-host restored tree differs\nwant=%#v\ngot=%#v", wantTree, got)
	}
	assertSemanticState(t, f.data, "A", 41)
	f.assertHealthy()
}

func TestManageSnapshotBackupAndRestoreFailureBoundariesRollback(t *testing.T) {
	f := newRecoveryFixture(t, "recovery-faults")
	writerHome, _, recipient := makeGPGWriterAndRecoveryHomes(t)
	t.Setenv("MEMA_TEST_DR_RECIPIENT", recipient)
	t.Setenv("MEMA_MANAGE_GPG_HOME", writerHome)
	localManifest := f.manifest(false)
	f.writeManifest(localManifest)
	beforeSnapshots := len(f.snapshotEntries())
	setManageFault(t, "snapshot-create")
	if err := manageSnapshot(localManifest, f.manifestPath, f.service, localScope()); err == nil {
		t.Fatal("injected snapshot creation interruption succeeded")
	}
	if got := len(f.snapshotEntries()); got != beforeSnapshots {
		t.Fatalf("interrupted snapshot became visible: before=%d after=%d", beforeSnapshots, got)
	}
	assertNoPartialSnapshotDirs(t, f.state)
	assertSemanticState(t, f.data, "A", 41)
	f.assertHealthy()

	remoteManifest := f.manifest(true)
	f.writeManifest(remoteManifest)
	for _, point := range []string{"snapshot-encrypted", "backup-before-promote", "backup-uploaded", "backup-metadata-publish"} {
		visibleBefore := len(f.snapshotEntries())
		setManageFault(t, point)
		if err := manageBackupWithOverrides(remoteManifest, f.manifestPath, f.service, localScope(), nil); err == nil {
			t.Fatalf("injected %s failure unexpectedly succeeded", point)
		}
		assertNoRemoteBackupArtifacts(t, f.remote)
		assertNoPartialSnapshotDirs(t, f.state)
		if got := len(f.snapshotEntries()); got != visibleBefore {
			t.Fatalf("failed %s backup became visible: before=%d after=%d", point, visibleBefore, got)
		}
		assertSemanticState(t, f.data, "A", 41)
		f.assertHealthy()
	}

	f.writeManifest(localManifest)
	if err := manageSnapshot(localManifest, f.manifestPath, f.service, localScope()); err != nil {
		t.Fatalf("create restore fixture snapshot: %v", err)
	}
	entries := f.snapshotEntries()
	var snapshot manageSnapshotMeta
	for _, entry := range entries {
		if entry.Encryption == "none" {
			snapshot = entry
		}
	}
	if snapshot.ID == "" {
		t.Fatal("fixture snapshot missing")
	}
	if err := f.populate("B", 900); err != nil {
		t.Fatal(err)
	}
	stateB := snapshotTree(t, f.data)
	assertSemanticState(t, f.data, "B", 900)
	for _, point := range []string{"restore-stage", "restore-before-promote", "restore-after-promote"} {
		setManageFault(t, point)
		if err := manageRestore(localManifest, f.manifestPath, f.service, snapshot.ID, localScope()); err == nil {
			t.Fatalf("injected %s failure unexpectedly restored", point)
		}
		if got := snapshotTree(t, f.data); fmt.Sprint(got) != fmt.Sprint(stateB) {
			t.Fatalf("%s failure did not preserve/rollback state B\nwant=%#v\ngot=%#v", point, stateB, got)
		}
		assertSemanticState(t, f.data, "B", 900)
		f.assertHealthy()
	}
	f.failRev = "A"
	if err := manageRestore(localManifest, f.manifestPath, f.service, snapshot.ID, localScope()); err == nil {
		t.Fatal("post-promotion health failure unexpectedly succeeded")
	}
	f.failRev = ""
	if got := snapshotTree(t, f.data); fmt.Sprint(got) != fmt.Sprint(stateB) {
		t.Fatalf("post-health-failure rollback did not restore B\nwant=%#v\ngot=%#v", stateB, got)
	}
	assertSemanticState(t, f.data, "B", 900)
	f.assertHealthy()
}

func TestManageSnapshotManifestAndArchiveRejectTamperingAndTraversal(t *testing.T) {
	f := newRecoveryFixture(t, "recovery-tamper")
	manifest := f.manifest(false)
	f.writeManifest(manifest)
	if err := manageSnapshot(manifest, f.manifestPath, f.service, localScope()); err != nil {
		t.Fatal(err)
	}
	meta := f.snapshotEntries()[0]
	for _, tamper := range []struct {
		name   string
		mutate func(*manageSnapshotMeta)
	}{
		{"negative ownership", func(value *manageSnapshotMeta) { value.Resources[0].UID = -1 }},
		{"special permission bits", func(value *manageSnapshotMeta) { value.Resources[0].Mode = 0o4777 }},
		{"archive path traversal", func(value *manageSnapshotMeta) { value.Resources[0].SnapshotPath = "../../outside" }},
		{"source path escape", func(value *manageSnapshotMeta) {
			value.Resources[0].Entries[0].Path = filepath.Join(filepath.Dir(f.data), "outside")
		}},
	} {
		copyBytes, _ := json.Marshal(meta)
		var changed manageSnapshotMeta
		_ = json.Unmarshal(copyBytes, &changed)
		tamper.mutate(&changed)
		if err := validateSnapshotResources(changed, manifest); err == nil {
			t.Fatalf("tampered %s metadata was accepted", tamper.name)
		}
	}
	dir := filepath.Join(f.state, f.service, "snapshots", meta.ID)
	payload := filepath.Join(dir, meta.PayloadFile)
	original, err := os.ReadFile(payload)
	if err != nil {
		t.Fatal(err)
	}
	malicious := rewriteArchiveWithMember(t, original, "filesystem/resources/9999/extra")
	if err := os.WriteFile(payload, malicious, 0o600); err != nil {
		t.Fatal(err)
	}
	meta.Size = int64(len(malicious))
	meta.PayloadSHA256 = snapshotSHA256(malicious)
	meta.CiphertextSHA256 = meta.PayloadSHA256
	if err := writeSnapshotMetaV2(dir, &meta); err != nil {
		t.Fatal(err)
	}
	if err := manageVerifySnapshot(f.service, meta.ID, localScope()); err == nil {
		t.Fatal("unexpected archive member was accepted")
	}

	for _, name := range []string{"../outside", "/absolute/escape"} {
		archive := makeTarArchive(t, name, tar.TypeReg, "bad")
		destination := t.TempDir()
		if _, err := extractSnapshotV2(archive, destination); err == nil {
			t.Fatalf("traversal archive member %q was accepted", name)
		}
	}
	archive := makeTarArchive(t, "filesystem/resources/link", tar.TypeSymlink, "")
	appendTarMember(t, archive, "filesystem/resources/link/pwned", tar.TypeReg, "bad")
	if _, err := extractSnapshotV2(archive, t.TempDir()); err == nil {
		t.Fatal("archive member nested under a symlink was accepted")
	}
}

func setManageFault(t *testing.T, point string) {
	t.Helper()
	previous := manageFaultInjector
	manageFaultInjector = func(candidate string) error {
		if candidate == point {
			return fmt.Errorf("injected failure at %s", point)
		}
		return nil
	}
	t.Cleanup(func() { manageFaultInjector = previous })
}

func assertNoPartialSnapshotDirs(t *testing.T, state string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(state, "*", "snapshots", ".partial-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("partial snapshot staging was not cleaned: %v %v", matches, err)
	}
}

func assertNoRemoteBackupArtifacts(t *testing.T, remote string) {
	t.Helper()
	files := 0
	err := filepath.WalkDir(remote, func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) && path == remote {
			return nil
		}
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files != 0 {
		t.Fatalf("failed backup left visible/pending remote files (%d)", files)
	}
}

func copySnapshotDirectory(source, destination string) error {
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		from, to := filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name())
		info, err := os.Lstat(from)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unexpected recovery bundle member %s", from)
		}
		data, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		if err := os.WriteFile(to, data, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func makeTarArchive(t *testing.T, name string, kind byte, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload.tar.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	if err := writeTarMember(tw, name, kind, content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendTarMember(t *testing.T, archive, name string, kind byte, content string) {
	t.Helper()
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	oldTar := tar.NewReader(reader)
	path := archive + ".new"
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(out)
	writer := tar.NewWriter(gz)
	for {
		h, err := oldTar.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		copyHeader := *h
		if err := writer.WriteHeader(&copyHeader); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg || h.Typeflag == tar.TypeRegA {
			if _, err := io.Copy(writer, oldTar); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writeTarMember(writer, name, kind, content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, archive); err != nil {
		t.Fatal(err)
	}
}

func rewriteArchiveWithMember(t *testing.T, original []byte, name string) []byte {
	t.Helper()
	archive := filepath.Join(t.TempDir(), "rewrite.tar.gz")
	if err := os.WriteFile(archive, original, 0o600); err != nil {
		t.Fatal(err)
	}
	appendTarMember(t, archive, name, tar.TypeReg, "unexpected")
	result, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func writeTarMember(writer *tar.Writer, name string, kind byte, content string) error {
	header := &tar.Header{Name: name, Mode: 0o600, Typeflag: kind, Size: int64(len(content)), Format: tar.FormatPAX}
	if kind == tar.TypeSymlink {
		header.Linkname, header.Size = "../../escape", 0
	}
	if kind == tar.TypeDir {
		header.Size = 0
	}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	if kind == tar.TypeReg || kind == tar.TypeRegA {
		_, err := io.WriteString(writer, content)
		return err
	}
	return nil
}

func TestSnapshotQuiesceRequiresDeclaredRunningSystemdService(t *testing.T) {
	// A declared stop-service policy without an active target must fail closed;
	// this unit test exercises only the adapter precondition, never host systemd.
	m := manageManifest{Version: 1, Service: "fixture", Snapshot: manageSnapshotPolicy{Consistency: "stop-service"}}
	_, err := managePrepareSnapshotV2(m)
	if err == nil || !strings.Contains(err.Error(), "requires a declared systemd unit") {
		t.Fatalf("stop-service consistency did not reject missing adapter target: %v", err)
	}
}

func TestManageSnapshotAnchoredExclusionOmitsOnlyResourceRootFile(t *testing.T) {
	f := newRecoveryFixture(t, "root-anchored-exclusion")
	if err := os.WriteFile(filepath.Join(f.data, ".tpahub.lock"), []byte("root lock"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.data, "nested", ".tpahub.lock"), []byte("nested state"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := f.manifest(false)
	m.Data[0].Exclude = append(m.Data[0].Exclude, "./.tpahub.lock")
	f.writeManifest(m)
	if err := f.call(func() error { return manageSnapshot(m, f.manifestPath, f.service, localScope()) }); err != nil {
		t.Fatalf("create snapshot with resource-root exclusion: %v", err)
	}
	meta := f.snapshotEntries()[0]
	rootLockExcluded := false
	for _, excluded := range meta.Excluded {
		if excluded.Path == filepath.Join(f.data, ".tpahub.lock") {
			rootLockExcluded = true
		}
		if excluded.Path == filepath.Join(f.data, "nested", ".tpahub.lock") {
			t.Fatalf("nested lock file was incorrectly excluded: %#v", meta.Excluded)
		}
	}
	if !rootLockExcluded {
		t.Fatalf("resource-root lock file was not recorded as excluded: %#v", meta.Excluded)
	}
	dir := filepath.Join(f.state, f.service, "snapshots", meta.ID)
	root, cleanup, err := recoverRootFromSnapshot(meta, dir)
	if err != nil {
		t.Fatalf("extract captured snapshot payload: %v", err)
	}
	defer cleanup()
	root = filepath.Join(root, filepath.FromSlash(meta.Resources[0].SnapshotPath))
	if _, err := os.Lstat(filepath.Join(root, ".tpahub.lock")); !os.IsNotExist(err) {
		t.Fatalf("resource-root lock file was captured: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "nested", ".tpahub.lock")); err != nil {
		t.Fatalf("nested lock file was incorrectly excluded: %v", err)
	}
}

func TestSnapshotRuntimeVersionAndManifestTamperDetection(t *testing.T) {
	f := newRecoveryFixture(t, "snapshot-manifest-check")
	m := f.manifest(false)
	f.writeManifest(m)
	if err := manageSnapshot(m, f.manifestPath, f.service, localScope()); err != nil {
		t.Fatal(err)
	}
	meta := f.snapshotEntries()[0]
	dir := filepath.Join(f.state, f.service, "snapshots", meta.ID)
	manifest := filepath.Join(dir, "manifest.json")
	b, _ := os.ReadFile(manifest)
	var value map[string]any
	if err := json.Unmarshal(b, &value); err != nil {
		t.Fatal(err)
	}
	value["snapshot_format"] = float64(77)
	modified, _ := json.Marshal(value)
	if err := os.WriteFile(manifest, modified, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSnapshotMeta(dir); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("manifest mutation not detected: %v", err)
	}
}
