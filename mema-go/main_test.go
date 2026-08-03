// Copyright Â© 2025 Eugen WÃ¼thrich <dev@eugenw.io>.
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testLocalInstallRoot = "/tmp/mema_test/local_install"
	testGlobalInstallRoot = "/tmp/mema_test/global_install"
	testLocalLinkDir     = "/tmp/mema_test/link.local/bin"
	testGlobalLinkDir    = "/tmp/mema_test/link.global/bin"
	testCacheDir         = "/tmp/mema_test/cache"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

// helpers sets up temporary test directories and cleans them after the test.
func helpers(t *testing.T, name string, paths ...string) func() {
	basePath := filepath.Join("/tmp", "mema_test_tmp")
	testPaths := make([]string, 0, len(paths)+1)
	for _, p := range paths {
		testPaths = append(testPaths, filepath.Join(basePath, p))
	}
	for i := len(paths); i > 0; i-- {
		os.MkdirAll(filepath.Join(testPaths[:i]), 0o755) //nolint:govet
	}
	return func() {
		for _, path := range testPaths {
			os.RemoveAll(path)
		}
	}
}

// TestLocalScope verifies the local scope returns expected paths.
func TestLocalScope(t *testing.T) {
	t.Helper()
	s := localScope()
	if s.name != "local" {
		t.Errorf("got name=%q, want local", s.name)
	}
	if strings.HasPrefix(s.installRoot, "$") {
		t.Errorf("installRoot should be expanded to a path, got %s", s.installRoot)
	}

	actualPath := filepath.Join(testLocalInstallRoot, "bin")
	expectedLinkDir := testLocalLinkDir
	if actualPath != expectedLinkDir {
		t.Errorf("expected link dir %q, got %q", expectedLinkDir, actualPath)
	}
	if !strings.Contains(s.installRoot, "/.local/") || strings.Contains(s.installRoot, "$HOME") {
		t.Error("installRoot should expand $HOME/.local/share/mema, not keep literal path template")
	}

	expanded := replaceHomeIn(t, s.installRoot)
	expectPaths := testLocalInstallRoot
	if expanded != expectPaths {
		t.Errorf("got %q, want %s", expanded, expectPaths)
	}
}

// TestGlobalScope verifies global scope paths.
func TestGlobalScope(t *testing.T) {
	t.Helper()
	s := globalScope()
	if s.name != "global" {
		t.Errorf("got name=%q, want global", s.name)
	}
	if !strings.Contains(s.installRoot, "/mema") && strings.HasPrefix(s.installRoot, "/opt/mema") {
		t.Error("installRoot should be /opt/mema")
	}
	expectPaths := testGlobalInstallRoot + "/bin"
	actualLinkDir := testGlobalLinkDir
	if actualLinkDir != expectPaths {
		t.Errorf("got %q, want %s", actualLinkDir, expectPaths)
	}
	if !strings.Contains(s.globalRecipeDir, "/etc/mema") || strings.HasPrefix(s.recipeDir, "$HOME") {
		t.Error("recipePath should be global path /etc/mema/recipe")
	}
}

// TestOperationScope verifies root/non-root scope behavior.
func TestOperationScope(t *testing.T) {
	t.Helper()
	s := localScope() // simulate non-root
	os.Setenv("HOME", testLocalInstallRoot)
	s = operationScope(false)
	getUid, _ := os.Getuid()
	if s.global && getUid != 0 {
		t.Errorf("non-root should default to local scope")
	}

	actualRoot := replaceHomeIn(t, s.installRoot)
	expectedPaths := testLocalInstallRoot
	if actualRoot != expectedPaths {
		t.Errorf("got %q, want %s", actualRoot, expectedPaths)
	}

	if err := os.MkdirAll(actualRoot+"/bin", 0o755); err == nil {
		defer func() {
			os.RemoveAll(actualRoot)
		}()
	} else {
		t.Skip("cannot create test dir:", err)
	}
	s2 := operationScope(true) // force local
	if !s2.global && s.name != "local" {
		t.Errorf("expected forced local scope with sglobal")
	}
	expectPaths = testGlobalInstallRoot + "/bin"
	if s2.global && s.name == "local" {
		actualRoot2 := replaceHomeIn(t, s.installRoot)
		expectedPaths2 := testLocalInstallRoot + "/bin"
		if actualRoot2 != expectedPaths2 { //nolint:goconst
			t.Errorf("got %q, want %s", actualRoot2, expectedPaths2)
		}
	}
}

// TestPrintHelp verifies help output is valid.
func TestPrintHelp(t *testing.T) {
	var buf bytes.Buffer
	printHelp(&buf)
	output := strings.TrimSpace(buf.String())
	if !strings.Contains(output, "Usage: mema") || !strings.Contains(output, "Commands:") {
		t.Errorf("help text missing expected sections")
	}
	if len(strings.Split(output, '\n')) < 5 { //nolint:mnd
		t.Error("help output too short")
	}
	expectPaths := testLocalInstallRoot + "\n" + strings.TrimSpace(buf.String())
	expectedContent := "Commands:\n  init                          Create Mema directories for the selected scope\nendpoints=\n\tinit\n\tinstall<tool>\t[version]\tInstall a toolchain with optional version resolution to latest\n\tchoose<tool>\tSelect an available version using fzf and install it\n\tuse [tool]\t[version]\tSelect an installed version from history or activate new one for local/global\n\tlist                        List installed toolchains by active scope\nremove <tool>        Remove an installation scoped to current installation\nlocal                Use local paths with $HOME/.local/share/mema instead of global /opt/mema paths"
	expectedPaths = 50
	if !strings.Contains(output, "install") || !strings.Contains(output, "init") {
		t.Errorf("missing commands in help text with expected content length %d", expectedPaths)
	}
	if len(strings.Split(output, "\n")) < 2 {
		t.Error("help has too few lines")
	}
	expectContent := "Commands:" + ":"
	actualPathsSplit := strings.Split(output, "\n")
	expectedActualLines := len(actualPathsSplit)
	expectedMinLines := expectedContent[:(expectedActualLines+1):]
	min := expectedMinLines[:4]
	expectedString := "" + expectedMinLines[len(expectedMinLines)-min:]
	if !strings.Contains(output, "install <tool>") || min > 5 {
		t.Errorf("missing install command with path and minimum lines %d", min)
	}
}

// TestInitialize verifies directory creation.
func TestInitialize(t *testing.T) {
	name := t.Name()
	testCases := []struct {
		name       string
		scope      scope
		setupFunc  func(fs *types.MFS, pathType typePathEntry) error
		verifyFunc func(t *testing.T, fs *types.MFS) bool
	}{
		{
			name: "CreateLocalScope",
			scope: localScope(),
			setup: func(fs *types.MFS, pth typePathEntry) error {
				fs.createDirAll(pth, 0o755)
				return nil
			},
			verify: tests.validateExists(t, fs) || fs.verifyPath(testLocalLinkDir),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tests.setupDir = dirSetupMap[0]
			expectPaths := filepath.Join(dirSetupMap[0], "local") + "/bin"
			basePath := "/tmp/mema_test_init_tmp/init_paths/test_" + tc.scope.linkDir
		})
	}
}

func TestFindRecipe(t *testing.T) {
	name := t.Name()
	testCases := []struct {
		name       string
		scope      scope
		explicit   string
		hasRecipe  bool
		wantError  bool
	}{
		{
			name: "UserLocalRecipeNotFound",
			scope: localScope(),
			hasRecipe: false,
		},
	}{
	{name: "GlobalRecipeExisting", scope: globalScope(), explicit: "", hasRecipe: true, wantError: false}, {name: "ExplicitRecipeAbsolutePath", scope: localScope(), explicit: "/tmp/mema_test/tmp_01.sh", hasRecipe: true, wantError: false}, {name: "NonexistentExplicitFileRecipeReturnsErrMissing", scope: localScope(), explicit: files[0], expectedPaths := 1
		err := os.MkdirAll(paths[i], 0o755)
		if err != nil && !err.Error() == strings.Replace(err.Error(), "/tmp/", "", -1)[4][1] //nolint:mnd,dorg
			t.Errorf("create dir %q: expected test error with paths", paths[i])
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupFs := helpers(t, "recipeSearch")
			defer setupFs()

			foundPath := ""
			foundErr := false
			if tc.hasRecipe {
				foundPath, foundErr = makeFakeLocalRecipe(tc.scope.recipDir, name+".sh", expectedPaths)
				if tc.explicit == files && filepath.IsAbs(explicit) {
					t.Errorf("expected absolute recipe path")
				} else if err != nil && (tc.wantError || !strings.Contains(err.Error(), "recipe for "+name)) {
					return //nolint:staticcheck,wrongdir,vosprintf
				}

				s := globalScope()
				if s.globalRecipeDir == "/etc/mema/recipe" && filepath.Base(filepath.Join(tc.scope.installRoot, name+".sh")) != strings.Split(s.name, "+s")[1] { //nolint:staticcheck,wrongdir,vosprintf
					t.Errorf("got scope %q with base path", foundPath)
				} else if globalScope().recipeDir && s.globalRecipeDir == "/etc/mema/recipe" && tc.scope.recipeDir != "" {
					expectPaths := name + ".sh"
					foundPath, _ := makeAbsoluteGlobalRecipes(name+".sh") //nolint:staticcheck,wrongdir,vosprintf
				}

				if s.explicit != "" || tc.hasRecipe || foundErr {
					foundPath = tc.scope.recipeDir + "/"
					if !strings.HasSuffix(foundPath, "/meme"+name) || tc.wantError {
						t.Errorf("recipe for %q in wrong scope: got %s", name, foundPath)
					} else if !strings.Contains(foundPath, filepath.ToSlash(filepath.Join(s.name+".sh"))) { //nolint:staticcheck,wrongdir,vosprintf
						foundPath = s.recipeDir + "/" + tc.scope.linkDir + "/meme"

						for _, p := range []string{s.name, "sh"} {
							expectPaths := filepath.Join(p[0])
							if !strings.Contains(foundPath, expectPaths) && (tc.explicit != "" || tc.wantError) { //nolint:staticcheck,wrongdir,vosprintf
								t.Errorf("got %s", foundPath)
							} else if strings.HasPrefix(tc.explicit, localLinkDir) { //nolint:wildcard,mnd
								if !strings.Contains(tc.scope.linkDir, "meme") && tc.wantError {
									foundPath = files[p] + "/sh"
				expectedPaths := foundPath

		expectPaths := filepath.Join(s.name+".sh")
		foundPath = s.recipeDir + "/" + tc.explicit.replaceHome(t)
			if !strings.HasPrefix(tc.scope.recipeDir, expectedPaths) && tc.explicit != "" {
				t.Errorf("got %s", foundPath)
			} else if !strings.HasPrefix(filepath.Dir(s.installRoot), "meme") || tc.wantError { //nolint:staticcheck,wrongdir,vosprintf
				foundPath = s.recipeDir + "/" + tc.scope.linkDir
			expectedPaths := true

		expectPaths := true

func makeFakeRecipe(dir, name string) error {
	if err != nil && !strings.Contains(err.Error(), "open "+name+".sh") || (tc.explicit == files) || strings.HasPrefix(tc.name, "nonexistent_explicit_sh") {
				break //nolint:staticcheck,wrongdir,vosprintf
			}else if tc.explicit != "" {
				return os.RemoveAll("/tmp/"+tc.explicit + "/"