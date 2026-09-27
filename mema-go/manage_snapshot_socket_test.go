package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func makeSnapshotTestSocket(t *testing.T, path string) net.Listener {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	unixListener := listener.(*net.UnixListener)
	unixListener.SetUnlinkOnClose(false)
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func makeSocketCaptureRoot(t *testing.T) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "gnupg")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "private.key"), []byte("persistent recovery key"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(root, "S.gpg-agent")
}

func TestManageSnapshotExcludesOnlyDeclaredUnixSockets(t *testing.T) {
	root, _ := makeSocketCaptureRoot(t)
	sockets := []string{"S.gpg-agent", "S.gpg-agent.browser", "S.gpg-agent.extra", "S.gpg-agent.ssh"}
	for _, name := range sockets {
		makeSnapshotTestSocket(t, filepath.Join(root, name))
	}
	destination := filepath.Join(t.TempDir(), "resource")
	captured, excluded, err := manageCaptureV2(root, destination, "resources/0000", nil, sockets)
	if err != nil {
		t.Fatal(err)
	}
	if len(excluded) != len(sockets) {
		t.Fatalf("got %d socket exclusions, want %d: %#v", len(excluded), len(sockets), excluded)
	}
	gotPaths := make([]string, 0, len(excluded))
	for _, item := range excluded {
		if item.Kind != "socket" {
			t.Fatalf("excluded entry has wrong type: %#v", item)
		}
		gotPaths = append(gotPaths, filepath.Base(item.Path))
		if _, err := os.Lstat(filepath.Join(destination, filepath.Base(item.Path))); !os.IsNotExist(err) {
			t.Fatalf("agent socket %q was captured: %v", item.Path, err)
		}
	}
	sort.Strings(gotPaths)
	wantPaths := append([]string(nil), sockets...)
	sort.Strings(wantPaths)
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("excluded socket paths=%v, want %v", gotPaths, wantPaths)
	}
	if _, err := os.Stat(filepath.Join(destination, "private.key")); err != nil {
		t.Fatalf("persistent GPG key material was not captured: %v", err)
	}
	meta := manageSnapshotMeta{SnapshotFormat: manageSnapshotFormatVersion, Resources: []manageCaptured{captured}, Excluded: excluded}
	manifest := manageManifest{Files: []manageResource{{Path: root, ExcludeSockets: sockets}}}
	if err := validateSnapshotResources(meta, manifest); err != nil {
		t.Fatalf("declared socket exclusion metadata did not validate: %v", err)
	}
	meta.Excluded[0].Kind = "file"
	if err := validateSnapshotResources(meta, manifest); err == nil {
		t.Fatal("invalid typed exclusion metadata was accepted")
	}
}

func TestManageSnapshotRecordsDeclaredSocketDisappearingDuringEnumeration(t *testing.T) {
	root, socketPath := makeSocketCaptureRoot(t)
	makeSnapshotTestSocket(t, socketPath)
	disappeared := false
	lstat := func(path string) (os.FileInfo, error) {
		if path == socketPath && !disappeared {
			disappeared = true
			if err := os.Remove(path); err != nil {
				return nil, err
			}
		}
		return os.Lstat(path)
	}
	captured, excluded, err := manageCaptureV2WithLstat(root, filepath.Join(t.TempDir(), "resource"), "resources/0000", nil, []string{"S.gpg-agent"}, lstat)
	if err != nil {
		t.Fatal(err)
	}
	if !disappeared || len(excluded) != 1 || excluded[0].Kind != "socket" || excluded[0].Path != socketPath {
		t.Fatalf("disappearing socket was not recorded: disappeared=%t excluded=%#v", disappeared, excluded)
	}
	if len(captured.Entries) != 1 || captured.Entries[0].Path != filepath.Join(root, "private.key") {
		t.Fatalf("persistent resource tree changed unexpectedly: %#v", captured.Entries)
	}
}

func TestManageSnapshotRejectsUnexpectedUnixSocket(t *testing.T) {
	root, _ := makeSocketCaptureRoot(t)
	makeSnapshotTestSocket(t, filepath.Join(root, "unexpected.sock"))
	if _, _, err := manageCaptureV2(root, filepath.Join(t.TempDir(), "resource"), "resources/0000", nil, []string{"S.gpg-agent"}); err == nil {
		t.Fatal("undeclared Unix socket was accepted")
	}
}

func TestManageSnapshotRejectsNonSocketAtDeclaredSocketPath(t *testing.T) {
	for _, socketName := range []string{"S.gpg-agent", "S.gpg-agent.browser", "S.gpg-agent.extra", "S.gpg-agent.ssh"} {
		for _, kind := range []string{"regular file", "symlink"} {
			t.Run(socketName+"/"+kind, func(t *testing.T) {
				root, _ := makeSocketCaptureRoot(t)
				socketPath := filepath.Join(root, socketName)
				if kind == "regular file" {
					if err := os.WriteFile(socketPath, []byte("not a socket"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink("private.key", socketPath); err != nil {
					t.Fatal(err)
				}
				if _, _, err := manageCaptureV2(root, filepath.Join(t.TempDir(), "resource"), "resources/0000", nil, []string{socketName}); err == nil {
					t.Fatalf("%s at declared socket path was accepted", kind)
				}
			})
		}
	}
}

func TestManageSnapshotSocketExclusionsAreExactSafeAndNonOverlapping(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name     string
		resource manageResource
	}{
		{"wildcard", manageResource{Path: root, ExcludeSockets: []string{"S.*"}}},
		{"traversal", manageResource{Path: root, ExcludeSockets: []string{"../S.gpg-agent"}}},
		{"absolute", manageResource{Path: root, ExcludeSockets: []string{"/run/agent.sock"}}},
		{"generic overlap", manageResource{Path: root, Exclude: []string{"S.gpg-agent"}, ExcludeSockets: []string{"S.gpg-agent"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateSnapshotResourceSet([]manageResource{test.resource}); err == nil {
				t.Fatal("unsafe socket exclusion was accepted")
			}
		})
	}
	if err := validateSnapshotResourceSet([]manageResource{{Path: root, ExcludeSockets: []string{"S.gpg-agent"}}}); err != nil {
		t.Fatalf("safe exact socket exclusion was rejected: %v", err)
	}
}

func TestManageSnapshotSocketDisappearanceRequiresENOENT(t *testing.T) {
	root, socketPath := makeSocketCaptureRoot(t)
	makeSnapshotTestSocket(t, socketPath)
	lstat := func(path string) (os.FileInfo, error) {
		if path == socketPath {
			return nil, errors.New("permission denied")
		}
		return os.Lstat(path)
	}
	if _, _, err := manageCaptureV2WithLstat(root, filepath.Join(t.TempDir(), "resource"), "resources/0000", nil, []string{"S.gpg-agent"}, lstat); err == nil {
		t.Fatal("non-ENOENT socket inspection failure was silently excluded")
	}
}
