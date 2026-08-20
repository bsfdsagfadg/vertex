package architectureguard

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTelemetryDoesNotReturn(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	forbidden := []string{
		"telemetry" + "_enabled",
		"telemetry" + ".Start",
		".telemetry" + "_state",
		".instance" + "_id",
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// Migration code must recognize and remove legacy telemetry keys;
			// that compatibility vocabulary is not a runtime telemetry feature.
			if rel == ".git" || rel == "docs" || rel == "test" || rel == "dist" || rel == filepath.Join("internal", "migration") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".go" && ext != ".js" && ext != ".json" && ext != ".yaml" && ext != ".yml" && ext != ".sh" && ext != ".ps1" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, needle := range forbidden {
			if strings.Contains(string(data), needle) {
				t.Errorf("forbidden telemetry artifact %q in %s", needle, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan repository: %v", err)
	}
}
