package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type manageVariableSource struct {
	Env string `json:"env,omitempty"`
}
type manageVariable struct {
	Type        string               `json:"type"`
	Default     any                  `json:"default,omitempty"`
	Overridable bool                 `json:"overridable,omitempty"`
	Source      manageVariableSource `json:"source,omitempty"`
	Enum        []string             `json:"enum,omitempty"`
	AllowedRoot string               `json:"allowed_root,omitempty"`
	Required    bool                 `json:"required,omitempty"`
}
type manageConfigFile struct {
	Variables map[string]json.RawMessage `json:"variables,omitempty"`
}
type manageResolvedValue struct {
	Value       string `json:"value,omitempty"`
	Source      string `json:"source"`
	Type        string `json:"type"`
	Secret      bool   `json:"secret,omitempty"`
	Overridable bool   `json:"overridable"`
}
type manageResolvedConfig struct {
	Values                map[string]manageResolvedValue
	Backup                manageBackupPolicy
	BackupRecipientSecret bool
	CleanGrace            time.Duration
}

var managePlaceholder = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func parseManageInvocation(args []string) (map[string]string, []string, error) {
	overrides := map[string]string{}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--set" {
			if i+1 >= len(args) {
				return nil, nil, errors.New("--set requires NAME=VALUE")
			}
			i++
			arg = args[i]
		} else if strings.HasPrefix(arg, "--set=") {
			arg = strings.TrimPrefix(arg, "--set=")
		} else {
			out = append(out, arg)
			continue
		}
		name, value, ok := strings.Cut(arg, "=")
		if !ok || !validVariableName(name) {
			return nil, nil, fmt.Errorf("invalid variable override %q", arg)
		}
		if _, exists := overrides[name]; exists {
			return nil, nil, fmt.Errorf("duplicate variable override %q", name)
		}
		overrides[name] = value
	}
	return overrides, out, nil
}
func validVariableName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if !(r == '_' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func resolveManageConfig(m manageManifest, manifestPath, service string, s scope, overrides map[string]string, builtins map[string]string) (manageResolvedConfig, error) {
	cfg := manageResolvedConfig{Values: map[string]manageResolvedValue{}}
	for name, variable := range m.Variables {
		if !validVariableName(name) || variable.Type == "" {
			return cfg, fmt.Errorf("invalid variable declaration %q", name)
		}
	}
	serviceValues, err := readManageLayer(serviceConfigPath(manifestPath, service))
	if err != nil {
		return cfg, err
	}
	hostValues, err := readManageLayer(hostConfigPath(manifestPath, s))
	if err != nil {
		return cfg, err
	}
	for name := range serviceValues {
		if _, ok := m.Variables[name]; !ok {
			return cfg, fmt.Errorf("service configuration contains unknown variable %q", name)
		}
	}
	for name := range hostValues {
		if _, ok := m.Variables[name]; !ok {
			return cfg, fmt.Errorf("host configuration contains unknown variable %q", name)
		}
	}
	for name := range overrides {
		if _, ok := m.Variables[name]; !ok {
			return cfg, fmt.Errorf("unknown variable %q", name)
		}
		if !m.Variables[name].Overridable {
			return cfg, fmt.Errorf("variable %q is not overridable", name)
		}
	}
	resolved := map[string]string{}
	sources := map[string]string{}
	visiting := map[string]bool{}
	var resolve func(string) (string, error)
	resolve = func(name string) (string, error) {
		if value, ok := resolved[name]; ok {
			return value, nil
		}
		variable, ok := m.Variables[name]
		if !ok {
			if value, ok := builtins[name]; ok {
				return value, nil
			}
			return "", fmt.Errorf("unknown variable %q", name)
		}
		if visiting[name] {
			return "", fmt.Errorf("variable expansion cycle at %q", name)
		}
		visiting[name] = true
		defer delete(visiting, name)
		raw, source := variable.Default, "manifest-default"
		if value, ok := serviceValues[name]; ok {
			raw, source = value, "service-config"
		}
		if value, ok := hostValues[name]; ok {
			raw, source = value, "host-config"
		}
		if variable.Source.Env != "" {
			if value, ok := os.LookupEnv(variable.Source.Env); ok {
				raw, source = value, "environment"
			}
		}
		if value, ok := overrides[name]; ok {
			raw, source = value, "invocation"
		}
		if raw == nil && variable.Required {
			return "", fmt.Errorf("required variable %q is missing", name)
		}
		value, err := manageValueString(raw)
		if err != nil {
			return "", fmt.Errorf("variable %q: %w", name, err)
		}
		value, err = expandManageString(value, func(ref string) (string, error) { return resolve(ref) }, builtins)
		if err != nil {
			return "", fmt.Errorf("variable %q: %w", name, err)
		}
		if err := validateManageVariable(variable, value); err != nil {
			return "", fmt.Errorf("variable %q: %w", name, err)
		}
		resolved[name], sources[name] = value, source
		return value, nil
	}
	for name, variable := range m.Variables {
		value, err := resolve(name)
		if err != nil {
			return cfg, err
		}
		cfg.Values[name] = manageResolvedValue{Value: value, Source: sources[name], Type: variable.Type, Secret: variable.Type == "secret-ref", Overridable: variable.Overridable}
	}
	if err := validateUnsupportedInterpolation(m); err != nil {
		return cfg, err
	}
	backup := m.Backup
	if backup.Format == "" {
		backup.Format = "mema-snapshot-v1"
	}
	if backup.Backend == "" {
		backup.Backend = m.Snapshot.Backend
	}
	if backup.Encryption.Recipient == "" {
		backup.Encryption.Recipient = m.Snapshot.Recipient
	}
	if backup.Encryption.Type == "" && backup.Encryption.Recipient != "" {
		backup.Encryption.Type = "gpg"
	}
	backup.Backend, err = expandManageString(backup.Backend, func(ref string) (string, error) { return resolvedManageRef(ref, cfg, builtins) }, builtins)
	if err != nil {
		return cfg, err
	}
	backup.File, err = expandManageString(backup.File, func(ref string) (string, error) { return resolvedManageRef(ref, cfg, builtins) }, builtins)
	if err != nil {
		return cfg, err
	}
	backup.Encryption.Recipient, err = expandManageString(backup.Encryption.Recipient, func(ref string) (string, error) { return resolvedManageRef(ref, cfg, builtins) }, builtins)
	if err != nil {
		return cfg, err
	}
	backup.Compression.Level, err = expandManageString(backup.Compression.Level, func(ref string) (string, error) { return resolvedManageRef(ref, cfg, builtins) }, builtins)
	if err != nil {
		return cfg, err
	}
	if backup.File == "" {
		backup.File = "${SERVICE}-${SNAPSHOT_ID}.mema"
		backup.File, err = expandManageString(backup.File, func(ref string) (string, error) { return resolvedManageRef(ref, cfg, builtins) }, builtins)
		if err != nil {
			return cfg, err
		}
	}
	if err := validateBackupPolicy(backup); err != nil {
		return cfg, err
	}
	if _, err := manageBackend(backup.Backend); err != nil {
		return cfg, err
	}
	cfg.Backup = backup
	for name, value := range cfg.Values {
		if value.Secret && strings.Contains(m.Backup.Encryption.Recipient, "${"+name+"}") {
			cfg.BackupRecipientSecret = true
		}
	}
	grace := m.Clean.GracePeriod
	if grace == "" {
		if value, ok := resolved["CLEAN_GRACE"]; ok {
			grace = value
		}
	}
	grace, err = expandManageString(grace, func(ref string) (string, error) { return resolvedManageRef(ref, cfg, builtins) }, builtins)
	if err != nil {
		return cfg, err
	}
	if grace == "" {
		grace = "7d"
	}
	cfg.CleanGrace, err = parseManageDuration(grace)
	if err != nil {
		return cfg, fmt.Errorf("clean grace: %w", err)
	}
	return cfg, nil
}
func resolvedManageRef(name string, cfg manageResolvedConfig, builtins map[string]string) (string, error) {
	if v, ok := cfg.Values[name]; ok {
		return v.Value, nil
	}
	if v, ok := builtins[name]; ok {
		return v, nil
	}
	return "", fmt.Errorf("unknown variable %q", name)
}
func serviceConfigPath(manifestPath, service string) string {
	if p := os.Getenv("MEMA_MANAGE_SERVICE_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(manifestPath), service+".local.json")
}
func hostConfigPath(manifestPath string, s scope) string {
	if p := os.Getenv("MEMA_MANAGE_HOST_CONFIG"); p != "" {
		return p
	}
	if os.Getenv("MEMA_MANAGE_MANIFEST_DIR") != "" {
		return filepath.Join(filepath.Dir(manifestPath), "host.json")
	}
	if s.global {
		return "/etc/mema/manage/host.json"
	}
	return filepath.Join(os.ExpandEnv(localInstallRoot), "manage", "host.json")
}
func readManageLayer(path string) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read configuration %s: %w", path, err)
	}
	var f manageConfigFile
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse configuration %s: %w", path, err)
	}
	for k, v := range f.Variables {
		if !validVariableName(k) {
			return nil, fmt.Errorf("invalid variable name %q", k)
		}
		out[k] = v
	}
	return out, nil
}
func manageValueString(raw any) (string, error) {
	if message, ok := raw.(json.RawMessage); ok {
		raw = json.RawMessage(message)
	}
	if message, ok := raw.(json.RawMessage); ok {
		if string(message) == "null" {
			return "", nil
		}
		var value any
		if err := json.Unmarshal(message, &value); err != nil {
			return "", err
		}
		return manageValueString(value)
	}
	if raw == nil {
		return "", nil
	}
	if s, ok := raw.(string); ok {
		return s, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
func expandManageString(value string, lookup func(string) (string, error), builtins map[string]string) (string, error) {
	return expandManageStringDepth(value, lookup, builtins, 0)
}
func expandManageStringDepth(value string, lookup func(string) (string, error), builtins map[string]string, depth int) (string, error) {
	if depth > 32 {
		return "", errors.New("variable expansion is too deep")
	}
	var err error
	out := managePlaceholder.ReplaceAllStringFunc(value, func(token string) string {
		name := token[2 : len(token)-1]
		if v, ok := builtins[name]; ok {
			return v
		}
		v, e := lookup(name)
		if e != nil {
			err = e
			return token
		}
		expanded, e := expandManageStringDepth(v, lookup, builtins, depth+1)
		if e != nil {
			err = e
			return token
		}
		return expanded
	})
	if err != nil {
		return "", err
	}
	if managePlaceholder.MatchString(out) {
		return expandManageStringDepth(out, lookup, builtins, depth+1)
	}
	return out, nil
}
func validateManageVariable(v manageVariable, value string) error {
	switch v.Type {
	case "string", "secret-ref", "backend-ref":
		if value == "" && v.Required {
			return errors.New("value is required")
		}
	case "bool":
		if value != "true" && value != "false" {
			return errors.New("expected boolean")
		}
	case "integer":
		if _, err := strconv.Atoi(value); err != nil {
			return errors.New("expected integer")
		}
	case "duration":
		if _, err := parseManageDuration(value); err != nil {
			return err
		}
	case "enum":
		ok := false
		for _, x := range v.Enum {
			if x == value {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("value is not an allowed enum")
		}
	case "path":
		if v.AllowedRoot == "" {
			return errors.New("path variables require allowed_root")
		}
		clean := filepath.Clean(value)
		root := filepath.Clean(v.AllowedRoot)
		if !filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || !(clean == root || strings.HasPrefix(clean, root+string(filepath.Separator))) {
			return errors.New("path is outside allowed_root")
		}
	default:
		return fmt.Errorf("unsupported variable type %q", v.Type)
	}
	return nil
}
func parseManageDuration(value string) (time.Duration, error) {
	if strings.HasSuffix(value, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err != nil || n < 0 {
			return 0, errors.New("invalid duration")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(value)
}
func validateBackupPolicy(b manageBackupPolicy) error {
	if b.Format != "mema-snapshot-v1" {
		return fmt.Errorf("unsupported backup format %q", b.Format)
	}
	if b.Encryption.Type != "" && b.Encryption.Type != "gpg" {
		return fmt.Errorf("unsupported backup encryption %q", b.Encryption.Type)
	}
	if b.Encryption.Type == "gpg" && b.Encryption.Recipient == "" {
		return errors.New("backup encryption recipient is missing")
	}
	if b.Compression.Type != "" && b.Compression.Type != "gzip" {
		return fmt.Errorf("unsupported backup compression %q", b.Compression.Type)
	}
	if strings.ContainsAny(b.File, "/\\\x00") || b.File == "." || b.File == ".." || strings.Contains(b.File, "..") {
		return errors.New("backup file must be a safe object name")
	}
	return nil
}
func validateUnsupportedInterpolation(m manageManifest) error {
	for _, r := range manageResources(m) {
		if strings.Contains(r.Path, "${") {
			return fmt.Errorf("variables are not supported in resource paths")
		}
	}
	if strings.Contains(m.Health.Ready, "${") || strings.Contains(m.Health.Version, "${") {
		return errors.New("variables are not supported in health targets")
	}
	if strings.Contains(m.Snapshot.Consistency, "${") {
		return errors.New("variables are not supported in snapshot consistency")
	}
	for _, t := range m.Targets {
		for _, u := range t.Units {
			if strings.Contains(u, "${") {
				return errors.New("variables are not supported in systemd targets")
			}
		}
		for _, p := range t.Configs {
			if strings.Contains(p, "${") {
				return errors.New("variables are not supported in nginx targets")
			}
		}
	}
	return nil
}
func manageConfigCommand(m manageManifest, path, service string, s scope, overrides map[string]string) error {
	cfg, err := resolveManageConfig(m, path, service, s, overrides, map[string]string{"SERVICE": service, "HOST": hostnameOrUnknown(), "SNAPSHOT_ID": "snapshot-pending", "TIMESTAMP": time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return err
	}
	safeBackup := cfg.Backup
	recipientExpression := m.Backup.Encryption.Recipient
	if recipientExpression == "" {
		recipientExpression = m.Snapshot.Recipient
	}
	for name, value := range cfg.Values {
		if value.Secret && strings.Contains(recipientExpression, "${"+name+"}") {
			safeBackup.Encryption.Recipient = "<configured>"
		}
	}
	out := map[string]any{"service": service, "variables": map[string]any{}, "backup": safeBackup}
	vars := out["variables"].(map[string]any)
	for name, v := range cfg.Values {
		entry := map[string]any{"type": v.Type, "source": v.Source, "overridable": v.Overridable}
		if v.Secret {
			if v.Value != "" {
				entry["value"] = "<configured>"
			} else {
				entry["value"] = "<missing>"
			}
			entry["secret"] = true
		} else {
			entry["value"] = v.Value
		}
		vars[name] = entry
	}
	return manageOutput(out)
}
func hostnameOrUnknown() string {
	h, e := os.Hostname()
	if e != nil {
		return "unknown"
	}
	return h
}
