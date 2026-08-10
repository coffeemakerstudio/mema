package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	localInstallRoot = "$HOME/.local/share/mema"
	localLinkDir     = "$HOME/.local/bin"
	localLibDir      = "$HOME/.local/share/mema/lib"
	localRecipeDir   = "$HOME/.local/share/mema/recipe"

	globalInstallRoot = "/opt/mema"
	globalLinkDir     = "/usr/local/bin"
	globalLibDir      = "/opt/mema/lib"
	globalRecipeDir   = "/etc/mema/recipe"

	cacheDir = "/tmp/mema/cache"
)

var errSelectionCanceled = errors.New("selection canceled")

type scope struct {
	name        string
	installRoot string
	linkDir     string
	libDir      string
	recipeDir   string
	global      bool
}

type installation struct {
	name    string
	version string
	scope   scope
}

func localScope() scope {
	return scope{
		name:        "local",
		installRoot: os.ExpandEnv(localInstallRoot),
		linkDir:     os.ExpandEnv(localLinkDir),
		libDir:      os.ExpandEnv(localLibDir),
		recipeDir:   os.ExpandEnv(localRecipeDir),
	}
}

func globalScope() scope {
	return scope{
		name:        "global",
		installRoot: globalInstallRoot,
		linkDir:     globalLinkDir,
		libDir:      globalLibDir,
		recipeDir:   globalRecipeDir,
		global:      true,
	}
}

func operationScope(local bool) scope {
	if !local && os.Geteuid() == 0 {
		return globalScope()
	}
	return localScope()
}

func printHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: mema <command> [options]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  init                         Create Mema directories for the selected scope")
	fmt.Fprintln(w, "  install <tool> [version]     Install a version; omitted version resolves to latest")
	fmt.Fprintln(w, "  choose <tool>                Select an available version with fzf and install it")
	fmt.Fprintln(w, "  use [tool] [version]         Select an installed version with fzf and activate it")
	fmt.Fprintln(w, "  list                         List installed toolchains and active versions")
	fmt.Fprintln(w, "  remove <tool> [version]      Remove an installed version")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Options:")
	fmt.Fprintln(w, "  --local                      Use the per-user Mema paths")
	fmt.Fprintln(w, "  --file <path>                Use a recipe file instead of an installed recipe")
}

func main() {
	if len(os.Args) < 2 || os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h" {
		printHelp(os.Stdout)
		return
	}

	command := os.Args[1]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	local := flags.Bool("local", false, "Use local scope for the operation")
	recipeFile := flags.String("file", "", "Specify a recipe file to install from")
	if err := flags.Parse(os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "mema: %v\n", err)
		printHelp(os.Stderr)
		os.Exit(2)
	}

	if err := run(command, flags.Args(), *local, *recipeFile); err != nil {
		if errors.Is(err, errSelectionCanceled) {
			return
		}
		fmt.Fprintf(os.Stderr, "mema: %v\n", err)
		os.Exit(1)
	}
}

func run(command string, args []string, local bool, recipeFile string) error {
	s := operationScope(local)

	switch command {
	case "init":
		if len(args) != 0 {
			return errors.New("init does not accept arguments")
		}
		return initialize(s)
	case "install":
		if len(args) < 1 || len(args) > 2 {
			return errors.New("usage: mema install <tool> [version]")
		}
		version := "latest"
		if len(args) == 2 {
			version = args[1]
		}
		return install(args[0], version, s, recipeFile)
	case "choose":
		if len(args) != 1 {
			return errors.New("usage: mema choose <tool>")
		}
		return choose(args[0], s, recipeFile)
	case "use":
		if len(args) > 2 {
			return errors.New("usage: mema use [tool] [version]")
		}
		return use(args, s, !local && !s.global)
	case "list":
		if len(args) != 0 {
			return errors.New("list does not accept arguments")
		}
		return list(s, !local && !s.global)
	case "remove":
		if len(args) < 1 || len(args) > 2 {
			return errors.New("usage: mema remove <tool> [version]")
		}
		return remove(args, s)
	default:
		return fmt.Errorf("unknown command %q; run 'mema help'", command)
	}
}

func initialize(s scope) error {
	for _, path := range []string{
		s.installRoot,
		s.linkDir,
		s.libDir,
		filepath.Join(s.libDir, "include"),
		filepath.Join(s.libDir, "lib"),
		filepath.Join(s.libDir, "pkgconfig"),
		filepath.Join(s.libDir, "share"),
		s.recipeDir,
		cacheDir,
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", path, err)
		}
	}
	return nil
}

func findRecipe(name string, s scope, explicit string) (string, error) {
	if err := validatePathComponent(name, "tool"); err != nil {
		return "", err
	}
	if explicit != "" {
		path, err := filepath.Abs(os.ExpandEnv(explicit))
		if err != nil {
			return "", fmt.Errorf("resolve recipe path: %w", err)
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			return "", fmt.Errorf("recipe %q not found", path)
		}
		return path, nil
	}

	paths := []string{
		filepath.Join(localScope().recipeDir, name+".sh"),
		filepath.Join(globalScope().recipeDir, name+".sh"),
		filepath.Join(s.recipeDir, name+".sh"),
	}
	seen := make(map[string]bool)
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("recipe for %q not found in %s or %s", name, localScope().recipeDir, globalScope().recipeDir)
}

func recipeEnv(s scope, installDir, version string) []string {
	env := append([]string{}, os.Environ()...)
	path := os.Getenv("PATH")
	if !strings.Contains(":"+path+":", ":/usr/local/bin:") {
		path = "/usr/local/bin:" + path
	}
	env = append(env,
		"PATH="+path,
		"MEMA_INSTALL_DIR="+installDir,
		"MEMA_VERSION="+version,
		"MEMA_CACHE="+cacheDir,
		"MEMA_LINK_DIR="+s.linkDir,
		"MEMA_LIB_DIR="+s.libDir,
		"MEMA_INCLUDE_DIR="+filepath.Join(s.libDir, "include"),
		"MEMA_LIB_LINK_DIR="+filepath.Join(s.libDir, "lib"),
		"MEMA_PKG_CONFIG_DIR="+filepath.Join(s.libDir, "pkgconfig"),
		"MEMA_SHARE_DIR="+filepath.Join(s.libDir, "share"),
	)
	if s.global && os.Geteuid() != 0 {
		env = append(env, "MEMA_SUDO=sudo")
	} else {
		env = append(env, "MEMA_SUDO=")
	}
	return env
}

func runRecipe(recipe, function string, env []string) error {
	cmd := exec.Command("bash", "-c", `source "$1"; "$2"`, "mema", recipe, function)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s from %s: %w", function, recipe, err)
	}
	return nil
}

func recipeOutput(recipe, script string) (string, error) {
	cmd := exec.Command("bash", "-c", script, "mema", recipe)
	cmd.Env = os.Environ()
	cmd.Stderr = os.Stderr
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("query %s: %w", recipe, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func resolveVersion(recipe, version string) (string, error) {
	if version != "latest" {
		return version, nil
	}
	output, err := recipeOutput(recipe, `source "$1"; if declare -F mema_resolve_version >/dev/null; then mema_resolve_version; else mema_get_versions | awk 'NR == 1 { print $1 }'; fi`)
	if err != nil {
		return "", err
	}
	if output == "" || strings.ContainsAny(output, " \t\n") {
		return "", fmt.Errorf("recipe %s did not resolve a latest version", recipe)
	}
	return output, nil
}

func install(name, version string, s scope, explicitRecipe string) error {
	if err := validatePathComponent(name, "tool"); err != nil {
		return err
	}
	recipe, err := findRecipe(name, s, explicitRecipe)
	if err != nil {
		return err
	}
	version, err = resolveVersion(recipe, version)
	if err != nil {
		return err
	}
	if err := validatePathComponent(version, "version"); err != nil {
		return err
	}
	var installDir string
	if strings.HasPrefix(name, "lib-") {
		installDir = filepath.Join(s.libDir, name, version)
	} else {
		installDir = filepath.Join(s.installRoot, name, version)
	}
	if err := os.MkdirAll(filepath.Dir(installDir), 0o755); err != nil {
		return fmt.Errorf("create tool directory: %w", err)
	}
	return runRecipe(recipe, "mema_install", recipeEnv(s, installDir, version))
}

func choose(name string, s scope, explicitRecipe string) error {
	recipe, err := findRecipe(name, s, explicitRecipe)
	if err != nil {
		return err
	}
	versions, err := recipeOutput(recipe, `source "$1"; mema_get_versions | awk '!seen[$1]++'`)
	if err != nil {
		return err
	}
	selection, err := fzfSelect(versions, "Mema versions for "+name)
	if err != nil {
		return err
	}
	fields := strings.Fields(selection)
	if len(fields) == 0 {
		return errors.New("selected version is empty")
	}
	return install(name, fields[0], s, recipe)
}

func fzfSelect(input, prompt string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", errors.New("no choices available")
	}
	if _, err := exec.LookPath("fzf"); err != nil {
		return "", errors.New("fzf is required for interactive selection")
	}
	cmd := exec.Command("fzf", "--height=40%", "--reverse", "--prompt", prompt+"> ")
	cmd.Stdin = strings.NewReader(input + "\n")
	cmd.Stderr = os.Stderr
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 130 {
			return "", errSelectionCanceled
		}
		return "", fmt.Errorf("run fzf: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func use(args []string, selected scope, local bool) error {
	installations, err := installed(selected, local)
	if err != nil {
		return err
	}
	if len(args) > 0 {
		installations = filterInstallations(installations, args[0], "")
	}
	if len(args) > 1 {
		installations = filterInstallations(installations, "", args[1])
	}
	if len(installations) == 0 {
		return errors.New("no matching installed toolchains")
	}

	choice := installations[0]
	if len(args) != 2 {
		labels := make([]string, 0, len(installations))
		byLabel := make(map[string]installation, len(installations))
		for _, item := range installations {
			label := strings.Join([]string{item.scope.name, item.name, item.version}, "\t")
			labels = append(labels, label)
			byLabel[label] = item
		}
		selection, err := fzfSelect(strings.Join(labels, "\n"), "Installed Mema toolchains")
		if err != nil {
			return err
		}
		var ok bool
		choice, ok = byLabel[selection]
		if !ok {
			return errors.New("selected toolchain is invalid")
		}
	}
	return activate(choice)
}

func installed(selected scope, includeGlobal bool) ([]installation, error) {
	scopes := []scope{selected}
	if includeGlobal && !selected.global {
		scopes = append([]scope{globalScope()}, scopes...)
	}
	seen := make(map[string]bool)
	var result []installation
	for _, s := range scopes {
		if seen[s.installRoot] {
			continue
		}
		seen[s.installRoot] = true
		tools, err := os.ReadDir(s.installRoot)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read %s: %w", s.installRoot, err)
		}
		for _, tool := range tools {
			if !tool.IsDir() {
				continue
			}
			versions, err := os.ReadDir(filepath.Join(s.installRoot, tool.Name()))
			if err != nil {
				return nil, fmt.Errorf("read versions for %s: %w", tool.Name(), err)
			}
			for _, version := range versions {
				if version.IsDir() {
					result = append(result, installation{name: tool.Name(), version: version.Name(), scope: s})
				}
			}
		}

		libs, err := os.ReadDir(s.libDir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read %s: %w", s.libDir, err)
		}
		for _, lib := range libs {
			if !lib.IsDir() || !strings.HasPrefix(lib.Name(), "lib-") {
				continue
			}
			versions, err := os.ReadDir(filepath.Join(s.libDir, lib.Name()))
			if err != nil {
				return nil, fmt.Errorf("read versions for %s: %w", lib.Name(), err)
			}
			for _, version := range versions {
				if version.IsDir() {
					result = append(result, installation{name: lib.Name(), version: version.Name(), scope: s})
				}
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.Join([]string{result[i].scope.name, result[i].name, result[i].version}, "\x00") < strings.Join([]string{result[j].scope.name, result[j].name, result[j].version}, "\x00")
	})
	return result, nil
}

func filterInstallations(items []installation, name, version string) []installation {
	filtered := make([]installation, 0, len(items))
	for _, item := range items {
		if name != "" && item.name != name {
			continue
		}
		if version != "" && item.version != version {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func activate(item installation) error {
	var installDir string
	if strings.HasPrefix(item.name, "lib-") {
		installDir = filepath.Join(item.scope.libDir, item.name, item.version)
	} else {
		installDir = filepath.Join(item.scope.installRoot, item.name, item.version)
	}
	recipe, err := findRecipe(item.name, item.scope, "")
	if err == nil {
		hasUse, checkErr := recipeHasFunction(recipe, "mema_use")
		if checkErr != nil {
			return checkErr
		}
		if hasUse {
			return runRecipe(recipe, "mema_use", recipeEnv(item.scope, installDir, item.version))
		}
	}
	return activateBinDirectory(item.scope, installDir)
}

func recipeHasFunction(recipe, function string) (bool, error) {
	cmd := exec.Command("bash", "-c", `source "$1"; declare -F "$2" >/dev/null`, "mema", recipe, function)
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("inspect %s: %w", recipe, err)
	}
	return true, nil
}

func activateBinDirectory(s scope, installDir string) error {
	binaries, err := os.ReadDir(filepath.Join(installDir, "bin"))
	if err != nil {
		return fmt.Errorf("recipe has no mema_use function and %s/bin cannot be read: %w", installDir, err)
	}
	for _, binary := range binaries {
		info, err := binary.Info()
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		if err := createLink(s, filepath.Join(installDir, "bin", binary.Name()), binary.Name()); err != nil {
			return err
		}
	}
	return nil
}

func createLink(s scope, source, name string) error {
	destination := filepath.Join(s.linkDir, name)
	if s.global && os.Geteuid() != 0 {
		cmd := exec.Command("sudo", "mkdir", "-p", s.linkDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("create link directory: %w", err)
		}
		cmd = exec.Command("sudo", "ln", "-sfn", source, destination)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("link %s: %w", name, err)
		}
		return nil
	}
	if err := os.MkdirAll(s.linkDir, 0o755); err != nil {
		return fmt.Errorf("create link directory: %w", err)
	}
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove existing link %s: %w", destination, err)
	}
	if err := os.Symlink(source, destination); err != nil {
		return fmt.Errorf("link %s: %w", name, err)
	}
	return nil
}

func list(s scope, includeGlobal bool) error {
	items, err := installed(s, includeGlobal)
	if err != nil {
		return err
	}
	fmt.Printf("%-15s | %-15s | %s\n", "PACKAGE", "ACTIVE", "VERSIONS")
	fmt.Println("--------------------------------------------------------------")
	byName := make(map[string][]installation)
	for _, item := range items {
		if item.name == "lib" || item.name == "recipe" {
			continue
		}
		key := item.scope.name + "\x00" + item.name
		byName[key] = append(byName[key], item)
	}
	keys := make([]string, 0, len(byName))
	for key := range byName {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := byName[key]
		versions := make([]string, 0, len(group))
		for _, item := range group {
			versions = append(versions, item.version)
		}
		sort.Strings(versions)
		name := group[0].name
		if len(keys) > 1 {
			name = group[0].scope.name + "/" + name
		}
		fmt.Printf("%-15s | %-15s | %s\n", name, activeVersion(group[0].scope, group[0].name), strings.Join(versions, ", "))
	}
	return nil
}

func activeVersion(s scope, name string) string {
	entries, err := os.ReadDir(s.linkDir)
	if err != nil {
		return "---"
	}
	base := s.installRoot
	if strings.HasPrefix(name, "lib-") {
		base = s.libDir
	}
	prefix := filepath.Join(base, name) + string(filepath.Separator)
	for _, entry := range entries {
		target, err := filepath.EvalSymlinks(filepath.Join(s.linkDir, entry.Name()))
		if err != nil || !strings.HasPrefix(target, prefix) {
			continue
		}
		relative, err := filepath.Rel(filepath.Join(base, name), target)
		if err != nil {
			continue
		}
		parts := strings.Split(relative, string(filepath.Separator))
		if len(parts) > 1 {
			return parts[0]
		}
	}
	return "---"
}

func remove(args []string, s scope) error {
	if err := validatePathComponent(args[0], "tool"); err != nil {
		return err
	}
	if len(args) == 2 {
		if err := validatePathComponent(args[1], "version"); err != nil {
			return err
		}
	}
	items, err := installed(s, false)
	if err != nil {
		return err
	}
	items = filterInstallations(items, args[0], "")
	if len(args) == 2 {
		items = filterInstallations(items, "", args[1])
	}
	if len(items) == 0 {
		return errors.New("no matching installed toolchains")
	}
	choice := items[0]
	if len(args) == 1 {
		labels := make([]string, 0, len(items))
		byLabel := make(map[string]installation, len(items))
		for _, item := range items {
			label := item.name + "\t" + item.version
			labels = append(labels, label)
			byLabel[label] = item
		}
		selection, err := fzfSelect(strings.Join(labels, "\n"), "Remove Mema toolchain")
		if err != nil {
			return err
		}
		choice = byLabel[selection]
	}
	return removeInstallation(choice)
}

func removeInstallation(item installation) error {
	var installDir string
	if strings.HasPrefix(item.name, "lib-") {
		installDir = filepath.Join(item.scope.libDir, item.name, item.version)
	} else {
		installDir = filepath.Join(item.scope.installRoot, item.name, item.version)
	}
	entries, err := os.ReadDir(item.scope.linkDir)
	if err == nil {
		for _, entry := range entries {
			link := filepath.Join(item.scope.linkDir, entry.Name())
			target, err := filepath.EvalSymlinks(link)
			if err != nil {
				// EvalSymlinks fails for dangling links; inspect the raw target so
				// stale links owned by this installation are still cleaned up.
				rawTarget, readErr := os.Readlink(link)
				if readErr == nil {
					target = rawTarget
				}
			}
			if strings.HasPrefix(target, installDir+string(filepath.Separator)) {
				if err := os.Remove(link); err != nil {
					return fmt.Errorf("remove link %s: %w", link, err)
				}
			}
		}
	}
	if err := os.RemoveAll(installDir); err != nil {
		return fmt.Errorf("remove %s: %w", installDir, err)
	}
	return nil
}

func validatePathComponent(value, label string) error {
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, `/\\`) {
		return fmt.Errorf("invalid %s %q", label, value)
	}
	return nil
}
