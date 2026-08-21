package legacy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConvertModelsUsesBuiltinDefaultsWithoutOverwritingExplicitFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	input := `{
  "models": [
    "gemini-3.7-flash",
    {"id":"gemini-3.7-flash","enabled":false},
    {"id":"gemini-3.7-flash","fake_stream_enabled":false,"trailing_fix_enabled":false},
    "custom-model"
  ],
  "alias_map": {}
}`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}

	converted, err := ConvertModels(path)
	if err != nil {
		t.Fatalf("ConvertModels: %v", err)
	}
	var document struct {
		Version int `json:"version"`
		Models  []struct {
			ID                 string `json:"id"`
			Enabled            bool   `json:"enabled"`
			FakeStreamEnabled  bool   `json:"fake_stream_enabled"`
			TrailingFixEnabled bool   `json:"trailing_fix_enabled"`
		} `json:"models"`
	}
	if err := json.Unmarshal(converted, &document); err != nil {
		t.Fatalf("parse converted models: %v", err)
	}
	if document.Version != 2 || len(document.Models) != 4 {
		t.Fatalf("unexpected converted document: %s", converted)
	}
	assertModelFlags(t, document.Models[0], true, true, true)
	assertModelFlags(t, document.Models[1], false, true, true)
	assertModelFlags(t, document.Models[2], true, false, false)
	assertModelFlags(t, document.Models[3], true, true, false)
}

func assertModelFlags(t *testing.T, model struct {
	ID                 string `json:"id"`
	Enabled            bool   `json:"enabled"`
	FakeStreamEnabled  bool   `json:"fake_stream_enabled"`
	TrailingFixEnabled bool   `json:"trailing_fix_enabled"`
}, enabled, fakeStream, trailingFix bool) {
	t.Helper()
	if model.Enabled != enabled || model.FakeStreamEnabled != fakeStream || model.TrailingFixEnabled != trailingFix {
		t.Fatalf("model %q flags=%+v, want enabled=%t fake_stream=%t trailing_fix=%t", model.ID, model, enabled, fakeStream, trailingFix)
	}
}
