package migration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotStagedFilesAllowsToolStateKey(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string]string{
		"config.json":    `{}`,
		"models.json":    `{}`,
		"data.db":        "database",
		"tool-state.key": "repository-key",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	files, err := snapshotStagedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateStagedFiles(files); err != nil {
		t.Fatalf("validate staged files: %v", err)
	}

	for _, file := range files {
		if file.Path == "tool-state.key" {
			if file.Kind != "state" {
				t.Fatalf("tool-state.key kind = %q, want state", file.Kind)
			}
			return
		}
	}
	t.Fatal("tool-state.key was not included in the staged snapshot")
}

func TestValidateStagedFilesRequiresToolStateKey(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string]string{
		"config.json": `{}`,
		"models.json": `{}`,
		"data.db":     "database",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	files, err := snapshotStagedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateStagedFiles(files); err == nil {
		t.Fatal("expected missing tool-state.key to be rejected")
	}
}
