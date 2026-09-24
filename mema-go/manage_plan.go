package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

type managePlan struct {
	Service            string               `json:"service"`
	Operation          string               `json:"operation"`
	Manifest           string               `json:"manifest"`
	DryRun             bool                 `json:"dry_run"`
	MutationsPerformed bool                 `json:"mutations_performed"`
	Configuration      map[string]any       `json:"configuration,omitempty"`
	Resources          []managePlanResource `json:"resources,omitempty"`
	Targets            []managePlanTarget   `json:"targets,omitempty"`
	Backup             map[string]any       `json:"backup,omitempty"`
	Snapshot           map[string]any       `json:"snapshot,omitempty"`
	Plan               []string             `json:"plan"`
}

type managePlanResource struct {
	Path        string `json:"path"`
	Role        string `json:"role,omitempty"`
	Type        string `json:"type,omitempty"`
	Present     bool   `json:"present"`
	Kind        string `json:"kind,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Owner       string `json:"owner,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Secret      bool   `json:"secret,omitempty"`
	Destination string `json:"destination,omitempty"`
	Conflict    bool   `json:"conflict,omitempty"`
}

type managePlanTarget struct {
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Units        []string          `json:"units,omitempty"`
	Configs      []string          `json:"configs,omitempty"`
	Active       map[string]string `json:"active,omitempty"`
	Enabled      map[string]string `json:"enabled,omitempty"`
	FragmentPath map[string]string `json:"fragment_path,omitempty"`
}

func managePlanSnapshot(m manageManifest, manifestPath, service string, s scope, overrides map[string]string, options manageInvocationOptions) error {
	builtins := map[string]string{"SERVICE": service, "SNAPSHOT_ID": "snapshot-pending", "TIMESTAMP": "plan", "HOST": hostnameOrUnknown()}
	cfg, err := resolveManageConfig(m, manifestPath, service, s, overrides, builtins)
	if err != nil {
		return err
	}
	plan := newManagePlan(service, "snapshot", manifestPath, cfg, m, options)
	plan.Resources = managePlanResources(m)
	plan.Targets = managePlanTargets(m)
	plan.Backup = managePlanBackup(cfg)
	plan.Plan = []string{
		"acquire the service lock",
		"verify declared resources",
		"record current runtime state",
	}
	if m.Snapshot.Consistency == "stop-service" {
		plan.Plan = append(plan.Plan, "stop declared systemd services if active")
	} else {
		plan.Plan = append(plan.Plan, "capture resources live without stopping the service")
	}
	plan.Plan = append(plan.Plan,
		"capture declared resources and write snapshot metadata",
		"encrypt the snapshot payload",
		"store or upload the encrypted payload",
		"verify stored ciphertext",
		"restore the previous service runtime state",
		"mark the snapshot verified",
	)
	return managePlanOutput(plan, options)
}

func managePlanRestore(m manageManifest, manifestPath, service, id string, s scope, overrides map[string]string, options manageInvocationOptions) error {
	cfg, err := resolveManageConfig(m, manifestPath, service, s, overrides, map[string]string{"SERVICE": service, "SNAPSHOT_ID": id, "TIMESTAMP": "plan", "HOST": hostnameOrUnknown()})
	if err != nil {
		return err
	}
	dir := filepath.Join(manageStateDir(s), service, "snapshots", id)
	meta, err := readSnapshotMeta(dir)
	if err != nil {
		return err
	}
	if meta.SnapshotFormat != manageSnapshotFormatVersion || !meta.Complete || meta.State != "verified" {
		return fmt.Errorf("snapshot %q is not verified and recoverable", id)
	}
	if err := validateSnapshotResources(meta, m); err != nil {
		return err
	}
	plan := newManagePlan(service, "restore", manifestPath, cfg, m, options)
	plan.Snapshot = map[string]any{
		"id": meta.ID, "format": meta.SnapshotFormat, "state": meta.State,
		"complete": meta.Complete, "encryption": meta.Encryption,
		"backend": meta.Backend, "ciphertext": meta.Ciphertext,
		"ciphertext_sha256": meta.CiphertextSHA256,
	}
	plan.Resources = make([]managePlanResource, 0, len(meta.Resources))
	for _, resource := range meta.Resources {
		_, statErr := os.Lstat(resource.Path)
		plan.Resources = append(plan.Resources, managePlanResource{
			Path: resource.Path, Present: statErr == nil, Kind: resource.Kind,
			Mode:   fmt.Sprintf("%04o", os.FileMode(resource.Mode).Perm()),
			SHA256: resource.SHA256, Destination: resource.Path,
			Conflict: statErr == nil,
		})
	}
	plan.Targets = managePlanTargets(m)
	plan.Backup = managePlanBackup(cfg)
	plan.Plan = []string{
		"acquire the service lock",
		"verify snapshot metadata, ciphertext, and resource checksums",
		"stop declared targets",
		"restore each snapshot resource to its manifest-owned destination",
		"reload and activate declared systemd/nginx targets",
		"run declared health checks",
	}
	return managePlanOutput(plan, options)
}

func managePlanClean(m manageManifest, manifestPath, service string, s scope, full bool, overrides map[string]string, options manageInvocationOptions) error {
	cfg, err := resolveManageConfig(m, manifestPath, service, s, overrides, map[string]string{"SERVICE": service, "SNAPSHOT_ID": "snapshot-pending", "TIMESTAMP": "plan", "HOST": hostnameOrUnknown()})
	if err != nil {
		return err
	}
	plan := newManagePlan(service, "clean", manifestPath, cfg, m, options)
	plan.Snapshot = map[string]any{"full": full, "grace_period": cfg.CleanGrace.String()}
	for _, resource := range m.Cleanup {
		if resource.Critical || resource.Clean == "never" {
			continue
		}
		if _, err := os.Lstat(resource.Path); err != nil {
			continue
		}
		plan.Resources = append(plan.Resources, managePlanResource{
			Path: resource.Path, Role: resource.Role, Type: resource.Type, Present: true,
			Destination: filepath.Join(manageStateDir(s), service, "quarantine", "<new-operation>", strings.TrimPrefix(resource.Path, "/")),
		})
	}
	q, err := manageReadQuarantine(service, s)
	if err != nil {
		return err
	}
	purge := []map[string]any{}
	for _, item := range q {
		entry := map[string]any{"path": item.Quarantine, "original": item.Original, "purge_after": item.PurgeAfter, "restored": item.Restored}
		if full && !item.Restored {
			protected := manageQuarantineProtected(item, service, s)
			entry["protected"] = protected
			if !protected {
				purge = append(purge, entry)
			}
		}
	}
	plan.Snapshot["quarantine_entries"] = q
	if full {
		plan.Snapshot["purge_candidates"] = purge
	}
	plan.Plan = []string{"acquire the service lock", "check snapshot/recovery coverage"}
	if len(plan.Resources) > 0 {
		plan.Plan = append(plan.Plan, "quarantine eligible cleanup resources")
	}
	if full {
		plan.Plan = append(plan.Plan, "purge expired, unprotected quarantine entries")
	}
	plan.Plan = append(plan.Plan, "write quarantine state")
	return managePlanOutput(plan, options)
}

func newManagePlan(service, operation, manifestPath string, cfg manageResolvedConfig, m manageManifest, options manageInvocationOptions) managePlan {
	return managePlan{
		Service: service, Operation: operation, Manifest: manifestPath,
		DryRun: options.dryRun, MutationsPerformed: false,
		Configuration: managePlanConfiguration(cfg),
	}
}

func managePlanConfiguration(cfg manageResolvedConfig) map[string]any {
	out := map[string]any{}
	names := make([]string, 0, len(cfg.Values))
	for name := range cfg.Values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := cfg.Values[name]
		entry := map[string]any{"source": value.Source, "type": value.Type, "overridable": value.Overridable}
		if value.Secret {
			entry["value"] = map[string]any{"configured": value.Value != "", "redacted": true}
		} else {
			entry["value"] = value.Value
		}
		out[name] = entry
	}
	return out
}

func managePlanBackup(cfg manageResolvedConfig) map[string]any {
	recipient := cfg.Backup.Encryption.Recipient
	if cfg.BackupRecipientSecret {
		recipient = "<configured>"
	}
	return map[string]any{
		"format": cfg.Backup.Format, "backend": cfg.Backup.Backend, "file": cfg.Backup.File,
		"encryption": cfg.Backup.Encryption.Type, "recipient": recipient,
		"compression": cfg.Backup.Compression.Type, "compression_level": cfg.Backup.Compression.Level,
		"clean_grace": cfg.CleanGrace.String(),
	}
}

func managePlanResources(m manageManifest) []managePlanResource {
	resources := make([]managePlanResource, 0, len(manageResources(m)))
	for _, resource := range manageResources(m) {
		item := managePlanResource{Path: resource.Path, Role: resource.Role, Type: resource.Type, Secret: resource.Secret}
		info, err := os.Lstat(resource.Path)
		if err != nil {
			resources = append(resources, item)
			continue
		}
		item.Present = true
		item.Mode = fmt.Sprintf("%04o", info.Mode().Perm())
		item.Kind = managePlanKind(info)
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			item.Owner = fmt.Sprintf("%d:%d", stat.Uid, stat.Gid)
		}
		if !resource.Secret && info.Mode().IsRegular() {
			item.SHA256 = manageHash(resource.Path)
		}
		resources = append(resources, item)
	}
	return resources
}

func managePlanKind(info os.FileInfo) string {
	if info.Mode()&os.ModeSymlink != 0 {
		return "symlink"
	}
	if info.IsDir() {
		return "directory"
	}
	if info.Mode().IsRegular() {
		return "file"
	}
	return info.Mode().String()
}

func managePlanTargets(m manageManifest) []managePlanTarget {
	names := make([]string, 0, len(m.Targets))
	for name := range m.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]managePlanTarget, 0, len(names))
	for _, name := range names {
		target := m.Targets[name]
		item := managePlanTarget{Name: name, Type: target.Type, Units: target.Units, Configs: target.Configs}
		if target.Type == "systemd" {
			item.Active, item.Enabled, item.FragmentPath = map[string]string{}, map[string]string{}, map[string]string{}
			for _, unit := range target.Units {
				item.Active[unit] = manageCommandFact("systemctl", "is-active", unit)
				item.Enabled[unit] = manageCommandFact("systemctl", "is-enabled", unit)
				item.FragmentPath[unit] = manageCommandFact("systemctl", "show", "-p", "FragmentPath", "--value", unit)
			}
		}
		out = append(out, item)
	}
	return out
}

func manageCommandFact(command string, args ...string) string {
	output, err := exec.Command(command, args...).Output()
	value := strings.TrimSpace(string(output))
	if value == "" && err != nil {
		return "unavailable"
	}
	if value == "" {
		return "unknown"
	}
	return value
}

func managePlanOutput(plan managePlan, options manageInvocationOptions) error {
	if manageJSON {
		data, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}
	fmt.Printf("Manage plan: %s %s\n", plan.Service, plan.Operation)
	fmt.Printf("Manifest: %s\n", plan.Manifest)
	fmt.Printf("Dry run: %t\n", plan.DryRun)
	fmt.Println("Configuration:")
	managePlanPrintJSON(plan.Configuration)
	fmt.Println("Backup:")
	managePlanPrintJSON(plan.Backup)
	fmt.Println("Resources:")
	for _, resource := range plan.Resources {
		status := "missing"
		if resource.Present {
			status = "present"
		}
		fmt.Printf("  %s [%s] %s\n", resource.Path, status, resource.Role)
	}
	fmt.Println("Targets:")
	managePlanPrintJSON(plan.Targets)
	fmt.Println("Plan:")
	for i, step := range plan.Plan {
		fmt.Printf("  %d. %s\n", i+1, step)
	}
	fmt.Println("Mutations performed: NONE")
	return nil
}

func managePlanPrintJSON(value any) {
	data, _ := json.MarshalIndent(value, "  ", "  ")
	fmt.Println(string(data))
}
