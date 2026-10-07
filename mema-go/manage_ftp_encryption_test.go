package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFTPCommandRootPrefixesConfiguredDestination(t *testing.T) {
	root, commands, err := ftpCommandRoot("ftp://ftp.example.invalid:2121/vserver/", []string{
		"RNFR service/payload.partial-snap-1",
		"RNTO service/payload",
		"DELE service/payload.manifest.gpg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if root != "ftp://ftp.example.invalid:2121/" {
		t.Fatalf("FTP control URL = %q", root)
	}
	want := []string{
		"RNFR vserver/service/payload.partial-snap-1",
		"RNTO vserver/service/payload",
		"DELE vserver/service/payload.manifest.gpg",
	}
	if fmt.Sprint(commands) != fmt.Sprint(want) {
		t.Fatalf("FTP control commands = %#v, want %#v", commands, want)
	}
	if _, _, err := ftpCommandRoot("ftp://ftp.example.invalid/../outside", []string{"DELE file"}); err == nil {
		t.Fatal("FTP backend path traversal was accepted")
	}
	for _, unsafe := range []string{"DELE ../outside", "DELE file\r\nRMD vserver"} {
		if _, _, err := ftpCommandRoot("ftp://ftp.example.invalid/vserver/", []string{unsafe}); err == nil {
			t.Fatalf("unsafe FTP control command accepted: %q", unsafe)
		}
	}
}

func TestFTPNetrcMachineOmitsConfiguredPort(t *testing.T) {
	host, err := ftpNetrcMachine("ftp://ftp.example.invalid:2121/account-root")
	if err != nil {
		t.Fatal(err)
	}
	if host != "ftp.example.invalid" {
		t.Fatalf("netrc machine includes a port or path: %q", host)
	}
	for _, invalid := range []string{"", "ftp://fixture-user@ftp.example.invalid/root", "https://ftp.example.invalid/root", "ftp://ftp.example.invalid/root?query=1"} {
		if _, err := ftpNetrcMachine(invalid); err == nil {
			t.Fatalf("invalid FTP URL accepted: %q", invalid)
		}
	}
}

func TestFTPTransferRejectsPlaintextBeforeNetworkOrCredentialRead(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	plain := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(plain, []byte(`{"resources":["/private/path"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := manageBackendConfig{
		Type: "ftp", URL: fmt.Sprintf("ftp://%s/", listener.Addr()),
		Username: "fixture-user", PasswordFile: filepath.Join(t.TempDir(), "missing-password"),
	}
	if err := ftpTransfer(backend, "service/manifest.json", plain, false); err == nil || !strings.Contains(err.Error(), "integrity-protected OpenPGP-encrypted artifact") {
		t.Fatalf("plaintext FTP upload was not rejected by the production boundary: %v", err)
	}
	if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if conn, err := listener.Accept(); err == nil {
		_ = conn.Close()
		t.Fatal("FTP network connection occurred before plaintext rejection")
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("unexpected listener result: %v", err)
	}
}

func TestFTPTransferAndControlCommandsIgnoreUserCurlConfig(t *testing.T) {
	writerHome, _, recipient := makeGPGWriterAndRecoveryHomes(t)
	t.Setenv("MEMA_MANAGE_GPG_HOME", writerHome)
	tmp := t.TempDir()
	plain := filepath.Join(tmp, "payload")
	cipher := filepath.Join(tmp, "payload.gpg")
	if err := os.WriteFile(plain, []byte("synthetic payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := encryptSnapshot(plain, cipher, recipient); err != nil {
		t.Fatal(err)
	}

	argsFile := filepath.Join(tmp, "curl-args")
	netrcFile := filepath.Join(tmp, "captured-netrc")
	fakeBin := filepath.Join(tmp, "bin")
	if err := os.Mkdir(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	fakeCurl := filepath.Join(fakeBin, "curl")
	script := `#!/bin/sh
printf '%s\n' "$@" > "$MEMA_TEST_CURL_ARGS"
previous=
for argument in "$@"; do
  if [ "$previous" = "--netrc-file" ]; then
    cp "$argument" "$MEMA_TEST_NETRC"
    exit $?
  fi
  previous=$argument
done
exit 2
`
	if err := os.WriteFile(fakeCurl, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MEMA_TEST_CURL_ARGS", argsFile)
	t.Setenv("MEMA_TEST_NETRC", netrcFile)
	passwordFile := filepath.Join(tmp, "password")
	if err := os.WriteFile(passwordFile, []byte("fixture-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := manageBackendConfig{
		Type: "ftp", URL: "ftp://ftp.example.invalid:2121/vserver/", Username: "fixture-user", PasswordFile: passwordFile,
	}
	if err := ftpTransfer(backend, "service/payload.gpg", cipher, false); err != nil {
		t.Fatalf("encrypted FTP transfer: %v", err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	argList := strings.Split(strings.TrimSuffix(string(args), "\n"), "\n")
	if len(argList) == 0 || argList[0] != "-q" || !strings.Contains(string(args), "ftp://ftp.example.invalid:2121/vserver/service/payload.gpg") {
		t.Fatalf("FTP transfer did not disable curl config or preserve the URL subpath: %q", args)
	}
	netrc, err := os.ReadFile(netrcFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(netrc), "machine ftp.example.invalid login fixture-user password fixture-password") || strings.Contains(string(netrc), ":2121") {
		t.Fatal("FTP transfer netrc did not use the URL hostname without its port")
	}

	if err := ftpCommand(backend, []string{"DELE service/payload.gpg"}); err != nil {
		t.Fatalf("FTP control command: %v", err)
	}
	args, err = os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(args), "-q\n") || !strings.Contains(string(args), "DELE vserver/service/payload.gpg") || !strings.Contains(string(args), "ftp://ftp.example.invalid:2121/\n") {
		t.Fatalf("FTP control transfer did not disable curl config or root commands at configured subpath: %q", args)
	}
}

func TestFTPEncryptedArtifactRequiresPublicKeyAndIntegrityProtection(t *testing.T) {
	writerHome, _, recipient := makeGPGWriterAndRecoveryHomes(t)
	t.Setenv("MEMA_MANAGE_GPG_HOME", writerHome)
	dir := t.TempDir()
	plain := filepath.Join(dir, "metadata.json")
	cipher := filepath.Join(dir, "metadata.gpg")
	if err := os.WriteFile(plain, []byte(`{"service":"synthetic"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateFTPEncryptedArtifact(plain); err == nil {
		t.Fatal("plaintext passed the FTP upload predicate")
	}
	if err := encryptSnapshot(plain, cipher, recipient); err != nil {
		t.Fatal(err)
	}
	if err := validateFTPEncryptedArtifact(cipher); err != nil {
		t.Fatalf("GPG encrypted, integrity-protected artifact rejected: %v", err)
	}
	weakRecoveryHome := filepath.Join(dir, "weak-recovery-home")
	if err := os.Mkdir(weakRecoveryHome, 0o700); err != nil {
		t.Fatal(err)
	}
	weakRecipient := "fixture-weak@example.invalid"
	cmd := exec.Command("gpg", "--batch", "--homedir", weakRecoveryHome, "--passphrase", "", "--quick-generate-key", weakRecipient, "rsa2048", "encr", "1d")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate RSA fixture recipient: %v: %s", err, output)
	}
	cmd = exec.Command("gpg", "--batch", "--homedir", weakRecoveryHome, "--armor", "--export", weakRecipient)
	publicKey, err := cmd.Output()
	if err != nil {
		t.Fatalf("export RSA fixture public key: %v", err)
	}
	cmd = exec.Command("gpg", "--batch", "--homedir", writerHome, "--import")
	cmd.Stdin = strings.NewReader(string(publicKey))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("import RSA fixture public key: %v: %s", err, output)
	}
	weakCipher := filepath.Join(dir, "metadata-unauthenticated.gpg")
	cmd = exec.Command("gpg", "--batch", "--yes", "--homedir", writerHome, "--trust-model", "always", "--rfc2440", "--disable-mdc", "--output", weakCipher, "--encrypt", "--recipient", weakRecipient, plain)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create legacy unauthenticated fixture: %v: %s", err, output)
	}
	packets, _ := exec.Command("gpg", "--batch", "--homedir", writerHome, "--list-packets", weakCipher).CombinedOutput()
	packetListing := string(packets)
	if !strings.Contains(packetListing, ":pubkey enc packet:") || !strings.Contains(packetListing, ":encrypted data packet:") {
		t.Fatalf("negative fixture is not public-key encrypted OpenPGP data: %s", packets)
	}
	if strings.Contains(packetListing, ":aead encrypted packet:") || openPGPMDCIntegrityProtected(packetListing) {
		t.Fatalf("negative fixture unexpectedly has integrity protection: %s", packets)
	}
	if err := validateFTPEncryptedArtifact(weakCipher); err == nil {
		t.Fatal("unauthenticated OpenPGP ciphertext passed the FTP upload predicate")
	}
}

func TestOpenPGPMDCIntegrityProtectedRequiresARealMethod(t *testing.T) {
	for _, test := range []struct {
		packets string
		want    bool
	}{
		{packets: "mdc_method: 2", want: true},
		{packets: "mdc_method: 0", want: false},
		{packets: "mdc_method: 20", want: false},
		{packets: ":encrypted data packet:", want: false},
	} {
		if got := openPGPMDCIntegrityProtected(test.packets); got != test.want {
			t.Errorf("openPGPMDCIntegrityProtected(%q) = %t, want %t", test.packets, got, test.want)
		}
	}
}

func TestLegacyRemoteMetadataVerificationRemainsSupported(t *testing.T) {
	root := t.TempDir()
	localDir := filepath.Join(root, "local")
	remoteRoot := filepath.Join(root, "remote")
	if err := os.MkdirAll(localDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"legacy":true}`)
	if err := os.WriteFile(filepath.Join(localDir, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestDigest := snapshotSHA256(manifest)
	if err := os.WriteFile(filepath.Join(localDir, "manifest.sha256"), []byte(manifestDigest+"  manifest.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := []byte("legacy encrypted payload fixture")
	payloadDigest := snapshotSHA256(payload)
	object := "service/legacy.payload.gpg"
	for relative, data := range map[string][]byte{
		object:                      payload,
		object + ".manifest.json":   manifest,
		object + ".manifest.sha256": []byte(manifestDigest + "  manifest.json\n"),
	} {
		path := filepath.Join(remoteRoot, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	backend := manageBackendConfig{Name: "legacy", Type: "local", Root: remoteRoot}
	meta := manageSnapshotMeta{Ciphertext: object, PayloadSHA256: payloadDigest}
	if err := verifyRemoteBackup(backend, meta, localDir); err != nil {
		t.Fatalf("legacy plaintext metadata layout no longer verifies: %v", err)
	}
}
