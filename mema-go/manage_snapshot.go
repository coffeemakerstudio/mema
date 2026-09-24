package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func writeSnapshotMeta(dir string, meta manageSnapshotMeta) error {
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o600)
}

type manageBackendConfig struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Root         string `json:"root,omitempty"`
	URL          string `json:"url,omitempty"`
	Username     string `json:"username,omitempty"`
	PasswordFile string `json:"password_file,omitempty"`
	TLS          bool   `json:"tls,omitempty"`
}

func manageBackend(name string) (manageBackendConfig, error) {
	if name == "" || name == "local" {
		return manageBackendConfig{Name: "local", Type: "local"}, nil
	}
	path := os.Getenv("MEMA_MANAGE_BACKENDS_FILE")
	if path == "" {
		path = "/etc/mema/backends.json"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return manageBackendConfig{}, fmt.Errorf("read backend configuration: %w", err)
	}
	var list []manageBackendConfig
	if err := json.Unmarshal(b, &list); err != nil {
		var object map[string]manageBackendConfig
		if err2 := json.Unmarshal(b, &object); err2 != nil {
			return manageBackendConfig{}, fmt.Errorf("parse backend configuration: %w", err)
		}
		for key, value := range object {
			if value.Name == "" {
				value.Name = key
			}
			list = append(list, value)
		}
	}
	for _, candidate := range list {
		if candidate.Name == name {
			if candidate.Type == "" {
				return manageBackendConfig{}, errors.New("backend type is missing")
			}
			return candidate, nil
		}
	}
	return manageBackendConfig{}, fmt.Errorf("backend %q is not configured", name)
}

func manageFinalizeSnapshot(meta manageSnapshotMeta, dir string, m manageManifest, cfg manageResolvedConfig, s scope) (manageSnapshotMeta, error) {
	backendName := cfg.Backup.Backend
	backend, err := manageBackend(backendName)
	if err != nil {
		return meta, err
	}
	meta.Backend = backend.Name
	meta.BackupFile = cfg.Backup.File
	meta.Compression = cfg.Backup.Compression.Type
	recipient := cfg.Backup.Encryption.Recipient
	remote := backend.Name != "local" || backend.Type != "local"
	if remote && recipient == "" {
		return meta, errors.New("remote snapshots require a configured encryption recipient")
	}
	if recipient == "" {
		meta.State = "verified"
		meta.Encryption = "none"
		meta.Complete = true
		return meta, nil
	}
	meta.State = "captured"
	plain := filepath.Join(dir, ".payload.tar.gz")
	cipher := filepath.Join(dir, "payload.gpg")
	if err := createSnapshotArchive(dir, plain); err != nil {
		return meta, err
	}
	defer os.Remove(plain)
	meta.Encryption = "gpg-public-key"
	if cfg.BackupRecipientSecret {
		meta.EncryptionRecipient = "<configured>"
	} else {
		meta.EncryptionRecipient = recipient
	}
	if err := encryptSnapshot(plain, cipher, recipient); err != nil {
		return meta, err
	}
	info, err := os.Stat(cipher)
	if err != nil {
		return meta, err
	}
	meta.Size = info.Size()
	meta.Ciphertext = "payload.gpg"
	meta.CiphertextSHA256 = manageHash(cipher)
	meta.State = "encrypted"
	if err := writeSnapshotMeta(dir, meta); err != nil {
		return meta, err
	}
	if remote {
		meta.State = "uploading"
		if err := writeSnapshotMeta(dir, meta); err != nil {
			return meta, err
		}
		object := filepath.ToSlash(filepath.Join(meta.Service, cfg.Backup.File))
		if err := backendPut(backend, object, cipher); err != nil {
			return meta, err
		}
		if err := backendVerify(backend, object, meta.CiphertextSHA256); err != nil {
			return meta, err
		}
		meta.Ciphertext = object
	}
	// The encrypted payload is the recovery copy. Remove the plaintext capture
	// and the local extracted tree so remote storage never receives plaintext.
	if err := os.RemoveAll(filepath.Join(dir, "filesystem")); err != nil {
		return meta, err
	}
	meta.State = "verified"
	meta.Complete = true
	if err := writeSnapshotMeta(dir, meta); err != nil {
		return meta, err
	}
	if remote {
		if err := backendPut(backend, meta.Ciphertext+".json", filepath.Join(dir, "manifest.json")); err != nil {
			return meta, err
		}
		if err := backendVerify(backend, meta.Ciphertext+".json", manageHash(filepath.Join(dir, "manifest.json"))); err != nil {
			return meta, err
		}
	}
	return meta, nil
}

func createSnapshotArchive(dir, output string) error {
	cmd := exec.Command("tar", "-C", dir, "-czf", output, "filesystem", "service-manifest.json")
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create snapshot archive: %w", err)
	}
	return nil
}
func encryptSnapshot(input, output, recipient string) error {
	args := []string{"--batch", "--yes", "--trust-model", "always", "--recipient", recipient, "--output", output, "--encrypt", input}
	cmd := exec.Command("gpg", args...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if home := os.Getenv("MEMA_MANAGE_GPG_HOME"); home != "" {
		cmd.Args = append([]string{"gpg", "--homedir", home}, args...)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("encrypt snapshot with established gpg tooling: %w", err)
	}
	return nil
}
func decryptSnapshot(input, output string) error {
	args := []string{"--batch", "--yes", "--output", output, "--decrypt", input}
	cmd := exec.Command("gpg", args...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if home := os.Getenv("MEMA_MANAGE_GPG_HOME"); home != "" {
		cmd.Args = append([]string{"gpg", "--homedir", home}, args...)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("decrypt snapshot: %w", err)
	}
	return nil
}

func validateBackendObject(object string) error {
	clean := filepath.Clean(filepath.FromSlash(object))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(filepath.FromSlash(object)) {
		return errors.New("unsafe snapshot backend object path")
	}
	return nil
}
func backendPut(backend manageBackendConfig, object, source string) error {
	if err := validateBackendObject(object); err != nil {
		return err
	}
	switch backend.Type {
	case "local":
		destination := filepath.Join(backend.Root, filepath.FromSlash(object))
		if backend.Root == "" {
			return errors.New("local backend root is empty")
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		return copyFile(source, destination)
	case "ftp":
		return ftpTransfer(backend, object, source, false)
	default:
		return fmt.Errorf("unsupported snapshot backend %q", backend.Type)
	}
}
func backendGet(backend manageBackendConfig, object, destination string) error {
	if err := validateBackendObject(object); err != nil {
		return err
	}
	switch backend.Type {
	case "local":
		return copyFile(filepath.Join(backend.Root, filepath.FromSlash(object)), destination)
	case "ftp":
		return ftpTransfer(backend, object, destination, true)
	default:
		return fmt.Errorf("unsupported snapshot backend %q", backend.Type)
	}
}
func backendVerify(backend manageBackendConfig, object, expected string) error {
	tmp, err := os.CreateTemp("", "mema-remote-verify-*")
	if err != nil {
		return err
	}
	path := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(path)
	if err := backendGet(backend, object, path); err != nil {
		return fmt.Errorf("download remote snapshot for verification: %w", err)
	}
	if got := manageHash(path); got != expected {
		return errors.New("remote ciphertext verification failed")
	}
	return nil
}
func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func ftpTransfer(backend manageBackendConfig, object, path string, download bool) error {
	if backend.URL == "" || strings.Contains(backend.URL, "@") {
		return errors.New("FTP backend requires a credential-free base URL")
	}
	password := ""
	if backend.PasswordFile != "" {
		b, err := os.ReadFile(backend.PasswordFile)
		if err != nil {
			return err
		}
		password = strings.TrimSpace(string(b))
	}
	netrc, err := os.CreateTemp("", "mema-ftp-netrc-")
	if err != nil {
		return err
	}
	netrcPath := netrc.Name()
	defer os.Remove(netrcPath)
	if err := netrc.Chmod(0o600); err != nil {
		return err
	}
	if backend.Username != "" {
		host := strings.TrimPrefix(strings.TrimPrefix(backend.URL, "ftp://"), "ftps://")
		host = strings.Split(host, "/")[0]
		if _, err := fmt.Fprintf(netrc, "machine %s login %s password %s\n", host, backend.Username, password); err != nil {
			return err
		}
	}
	_ = netrc.Close()
	url := strings.TrimRight(backend.URL, "/") + "/" + strings.TrimLeft(object, "/")
	args := []string{"--fail", "--silent", "--show-error", "--netrc-file", netrcPath}
	if backend.TLS {
		args = append(args, "--ftp-ssl")
	}
	if download {
		args = append(args, "--output", path, url)
	} else {
		args = append(args, "--ftp-create-dirs", "--upload-file", path, url)
	}
	cmd := exec.Command("curl", args...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		return errors.New("FTP snapshot transfer failed")
	}
	return nil
}

func manageEnsureSnapshotPayload(meta manageSnapshotMeta, dir string) (string, func(), error) {
	if meta.SnapshotFormat != manageSnapshotFormatVersion {
		return "", func() {}, fmt.Errorf("unsupported snapshot format %d", meta.SnapshotFormat)
	}
	if meta.State != "verified" || !meta.Complete {
		return "", func() {}, errors.New("snapshot is not verified and recoverable")
	}
	root := filepath.Join(dir, "filesystem")
	if meta.Encryption == "none" {
		if _, err := os.Stat(root); err != nil {
			return "", func() {}, err
		}
		return root, func() {}, nil
	}
	backend, err := manageBackend(meta.Backend)
	if err != nil {
		return "", func() {}, err
	}
	cipher := filepath.Join(dir, "payload.gpg")
	cipherTemp := ""
	if _, err := os.Stat(cipher); err != nil {
		if meta.Ciphertext == "" {
			return "", func() {}, errors.New("encrypted snapshot payload is missing")
		}
		tmpCipher, err := os.CreateTemp("", "mema-snapshot-")
		if err != nil {
			return "", func() {}, err
		}
		path := tmpCipher.Name()
		_ = tmpCipher.Close()
		if err := backendGet(backend, meta.Ciphertext, path); err != nil {
			os.Remove(path)
			return "", func() {}, err
		}
		cipher = path
		cipherTemp = path
	}
	if expected := meta.CiphertextSHA256; expected != "" && manageHash(cipher) != expected {
		if cipherTemp != "" {
			os.Remove(cipherTemp)
		}
		return "", func() {}, errors.New("snapshot ciphertext checksum mismatch")
	}
	tmp, err := os.MkdirTemp("", "mema-recover-")
	if err != nil {
		if cipherTemp != "" {
			os.Remove(cipherTemp)
		}
		return "", func() {}, err
	}
	plain := filepath.Join(tmp, "payload.tar.gz")
	if err := decryptSnapshot(cipher, plain); err != nil {
		os.RemoveAll(tmp)
		if cipherTemp != "" {
			os.Remove(cipherTemp)
		}
		return "", func() {}, err
	}
	if err := safeExtractSnapshot(plain, tmp); err != nil {
		os.RemoveAll(tmp)
		if cipherTemp != "" {
			os.Remove(cipherTemp)
		}
		return "", func() {}, err
	}
	root = filepath.Join(tmp, "filesystem")
	if _, err := os.Stat(root); err != nil {
		os.RemoveAll(tmp)
		if cipherTemp != "" {
			os.Remove(cipherTemp)
		}
		return "", func() {}, errors.New("decrypted snapshot has no filesystem payload")
	}
	return root, func() {
		os.RemoveAll(tmp)
		if cipherTemp != "" {
			os.Remove(cipherTemp)
		}
	}, nil
}
func safeExtractSnapshot(archive, destination string) error {
	listing, err := exec.Command("tar", "-tzf", archive).Output()
	if err != nil {
		return fmt.Errorf("validate snapshot archive: %w", err)
	}
	for _, entry := range strings.Split(strings.TrimSpace(string(listing)), "\n") {
		name := filepath.Clean(entry)
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return errors.New("snapshot archive contains unsafe path")
		}
	}
	cmd := exec.Command("tar", "-xzf", archive, "-C", destination)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("extract snapshot archive: %w", err)
	}
	return nil
}
func validateSnapshotResources(meta manageSnapshotMeta, manifest manageManifest) error {
	declared := map[string]bool{}
	for _, resource := range manageResources(manifest) {
		declared[resource.Path] = true
	}
	for _, resource := range meta.Resources {
		if !declared[resource.Path] {
			return fmt.Errorf("snapshot resource is not declared by manifest: %s", resource.Path)
		}
		clean := filepath.Clean(filepath.FromSlash(resource.SnapshotPath))
		if clean == "." || filepath.IsAbs(filepath.FromSlash(resource.SnapshotPath)) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return errors.New("snapshot contains unsafe resource path")
		}
	}
	return nil
}
func verifySnapshotPayload(meta manageSnapshotMeta, root string) error {
	for _, c := range meta.Resources {
		p := filepath.Join(root, filepath.FromSlash(c.SnapshotPath))
		info, err := os.Lstat(p)
		if err != nil {
			return fmt.Errorf("snapshot resource missing: %s", c.Path)
		}
		if c.Kind == "symlink" {
			link, e := os.Readlink(p)
			if e != nil || link != c.Link {
				return fmt.Errorf("snapshot symlink mismatch: %s", c.Path)
			}
		}
		if c.Kind == "file" && c.SHA256 != "" && manageHash(p) != c.SHA256 {
			return fmt.Errorf("snapshot resource checksum mismatch: %s", c.Path)
		}
		_ = info
	}
	return nil
}
