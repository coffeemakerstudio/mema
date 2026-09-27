package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSnapshotExclusionCanBeAnchoredToResourceRoot(t *testing.T) {
	patterns := []string{"./.tpahub.lock"}
	if !matchesSnapshotExclusion(".tpahub.lock", patterns) {
		t.Fatal("root-level lock file was not excluded")
	}
	if matchesSnapshotExclusion("nested/.tpahub.lock", patterns) {
		t.Fatal("root-anchored exclusion matched a nested lock file")
	}
	if !matchesSnapshotExclusion("nested/.tpahub.lock", []string{".tpahub.lock"}) {
		t.Fatal("unanchored basename exclusion no longer matches nested files")
	}
}

func TestNewSnapshotWriterRejectsV1Manifest(t *testing.T) {
	f := newRecoveryFixture(t, "v1-writer-policy")
	m := f.manifest(false)
	m.Backup.Format = "mema-snapshot-v1"
	f.writeManifest(m)

	err := manageSnapshot(m, f.manifestPath, f.service, localScope())
	if err == nil || !strings.Contains(err.Error(), "new snapshots require backup format mema-snapshot-v2") {
		t.Fatalf("v1 manifest unexpectedly created a new snapshot: %v", err)
	}
	if entries := f.snapshotEntries(); len(entries) != 0 {
		t.Fatalf("v1 manifest failure left published snapshot entries: %#v", entries)
	}
}

func TestRestoreManifestsCompatibleAcrossSnapshotFormatUpgrade(t *testing.T) {
	var v1, v2 manageManifest
	for manifest, format := range map[*manageManifest]string{&v1: "mema-snapshot-v1", &v2: "mema-snapshot-v2"} {
		data := []byte(`{"version":1,"service":"tparun","data":[{"path":"/var/lib/tparun/data"}],"backup":{"format":"` + format + `","backend":"local","file":"${SERVICE}-${SNAPSHOT_ID}.mema"}}`)
		if err := json.Unmarshal(data, manifest); err != nil {
			t.Fatal(err)
		}
	}
	v1.Backup.Encryption = manageEncryptionPolicy{}
	v2.Backup.Encryption = manageEncryptionPolicy{Type: "gpg", Recipient: "fixture@example.invalid"}
	v2.Data[0].Exclude = []string{".tpahub.lock"}
	if !restoreManifestsCompatible(v2, v1, 1) {
		t.Fatal("historical v1 manifest was rejected under the v2 backup policy")
	}
	if !restoreManifestsCompatible(v1, v2, 2) {
		t.Fatal("v2 manifest was rejected under the historical v1 backup policy")
	}

	changed := v2
	changed.Data = append([]manageResource(nil), v2.Data...)
	changed.Data[0].Path = "/var/lib/tparun/other"
	if restoreManifestsCompatible(changed, v1, 1) {
		t.Fatal("manifest resource change was incorrectly accepted as a format-only upgrade")
	}
	if restoreManifestsCompatible(v2, v1, 2) {
		t.Fatal("v1 embedded policy was accepted for a v2 snapshot")
	}
	changed = v1
	changed.Backup.Format = "mema-snapshot-v99"
	if restoreManifestsCompatible(v2, changed, 1) {
		t.Fatal("unsupported embedded backup format was accepted")
	}

	clone := func(source manageManifest) manageManifest {
		data, err := json.Marshal(source)
		if err != nil {
			t.Fatal(err)
		}
		var result manageManifest
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	resourceChanges := []struct {
		name   string
		mutate func(*manageManifest)
	}{
		{"add-root", func(m *manageManifest) { m.Data = append(m.Data, manageResource{Path: "/var/lib/tparun/extra"}) }},
		{"change-role", func(m *manageManifest) { m.Data[0].Role = "different-state" }},
		{"change-target", func(m *manageManifest) {
			m.Targets = map[string]manageTarget{"runtime": {Type: "systemd", Units: []string{"other.service"}}}
		}},
		{"change-health", func(m *manageManifest) { m.Health.Ready = "http://127.0.0.1:9999/ready" }},
	}
	for _, test := range resourceChanges {
		t.Run(test.name, func(t *testing.T) {
			current := clone(v2)
			test.mutate(&current)
			if restoreManifestsCompatible(current, v1, 1) {
				t.Fatalf("manifest change %q was treated as backup-policy-only", test.name)
			}
		})
	}
}
