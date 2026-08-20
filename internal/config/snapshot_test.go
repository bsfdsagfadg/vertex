package config

import (
	"context"
	"testing"
)

func TestStaticProviderCollectionsAreImmutable(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SafetySettings = map[string]string{"x": "y"}
	cfg.CustomBgPresets = []string{"one"}
	provider := StaticProvider(cfg)

	provider.SafetySettings()["x"] = "changed"
	provider.CustomBgPresets()[0] = "changed"
	aliases := provider.AliasMap()
	aliases["new"] = "model"
	models := provider.ModelRegistry()
	models[0].ID = "changed"

	if provider.SafetySettings()["x"] != "y" || provider.CustomBgPresets()[0] != "one" {
		t.Fatal("config collections escaped immutable snapshot")
	}
	if _, ok := provider.AliasMap()["new"]; ok || provider.ModelRegistry()[0].ID == "changed" {
		t.Fatal("model collections escaped immutable snapshot")
	}
}

func TestWithSnapshotKeepsOriginalView(t *testing.T) {
	first := DefaultConfig()
	first.MaxRetries = 1
	second := DefaultConfig()
	second.MaxRetries = 9

	ctx := WithSnapshot(context.Background(), StaticProvider(first))
	ctx = WithSnapshot(ctx, StaticProvider(second))
	if got := FromContext(ctx, StaticProvider(second)).MaxRetries(); got != 1 {
		t.Fatalf("snapshot changed in flight: got %d, want 1", got)
	}
}
