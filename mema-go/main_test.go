package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePathComponent(t *testing.T) {
	valid := []string{"go", "1.26.5", "tool+debug"}
	for _, value := range valid {
		if err := validatePathComponent(value, "value"); err != nil {
			t.Errorf("validatePathComponent(%q) returned error: %v", value, err)
		}
	}

	invalid := []string{"", ".", "..", "../escape", `tool\escape`}
	for _, value := range invalid {
		if err := validatePathComponent(value, "value"); err == nil {
			t.Errorf("validatePathComponent(%q) accepted unsafe value", value)
		}
	}
}

func TestResolveVersionFromRecipe(t *testing.T) {
	recipe := filepath.Join(t.TempDir(), "recipe.sh")
	content := "mema_resolve_version() { printf '%s\\n' '1.2.3'; }\n"
	if err := os.WriteFile(recipe, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	version, err := resolveVersion(recipe, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.2.3" {
		t.Fatalf("resolved version = %q, want %q", version, "1.2.3")
	}
}

func TestResolveVersionRejectsNonScalarOutput(t *testing.T) {
	recipe := filepath.Join(t.TempDir(), "recipe.sh")
	content := "mema_resolve_version() { printf '%s\\n' '1.2.3 1.2.4'; }\n"
	if err := os.WriteFile(recipe, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := resolveVersion(recipe, "latest"); err == nil {
		t.Fatal("resolveVersion accepted multiple values")
	}
}

func TestFilterInstallations(t *testing.T) {
	items := []installation{
		{name: "go", version: "1.26.4"},
		{name: "go", version: "1.26.5"},
		{name: "rust", version: "1.88.0"},
	}
	filtered := filterInstallations(items, "go", "1.26.5")
	if len(filtered) != 1 || filtered[0].version != "1.26.5" {
		t.Fatalf("filtered installations = %#v", filtered)
	}
}

func TestRemoveInstallationRemovesDanglingManagedLinks(t *testing.T) {
	root := t.TempDir()
	s := scope{
		name:        "local",
		installRoot: filepath.Join(root, "install"),
		linkDir:     filepath.Join(root, "bin"),
		libDir:      filepath.Join(root, "lib"),
	}
	item := installation{name: "go", version: "1.0.0", scope: s}
	installDir := filepath.Join(s.installRoot, item.name, item.version)
	if err := os.MkdirAll(s.linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(installDir, "bin", "go"), filepath.Join(s.linkDir, "go")); err != nil {
		t.Fatal(err)
	}
	if err := removeInstallation(item); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(s.linkDir, "go")); !os.IsNotExist(err) {
		t.Fatalf("managed dangling link still exists, err = %v", err)
	}
}
