package api

import (
	"path/filepath"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// ---- stripFakePrefix：剥离 "假流式-" / "fake-" 前缀 ----

func TestStripFakePrefix(t *testing.T) {
	fakePrefixes := []string{"假流式-", "fake-"}
	cases := []struct {
		name      string
		in        string
		wantModel string
		wantFake  bool
	}{
		{"chinese prefix", "假流式-gemini-2.5-flash", "gemini-2.5-flash", true},
		{"ascii prefix", "fake-gemini-2.5-pro", "gemini-2.5-pro", true},
		{"ascii prefix short", "fake-x", "x", true},
		{"no prefix passthrough", "gemini-2.5-flash", "gemini-2.5-flash", false},
		{"empty passthrough", "", "", false},
		{"prefix-like but not match", "fakegemini", "fakegemini", false},
		{"chinese prefix only", "假流式-", "", true},
		{"prefix inside name not stripped", "gemini-fake-thing", "gemini-fake-thing", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotModel, gotFake := stripFakePrefix(c.in, fakePrefixes)
			if gotModel != c.wantModel || gotFake != c.wantFake {
				t.Errorf("stripFakePrefix(%q)=(%q,%v)，期望 (%q,%v)",
					c.in, gotModel, gotFake, c.wantModel, c.wantFake)
			}
		})
	}
}

func TestResolveConfiguredModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	t.Setenv("VPROXY_MODELS", path)
	config.InvalidateModelsCache()
	if err := config.WriteModelRegistry([]config.ModelEntry{
		{ID: "enabled", Enabled: true, FakeStreamEnabled: true},
		{ID: "no-fake", Enabled: true, FakeStreamEnabled: false},
		{ID: "disabled", Enabled: false, FakeStreamEnabled: true},
	}, map[string]string{"alias": "enabled", "off-alias": "disabled"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(config.InvalidateModelsCache)

	cfg := config.DefaultConfig()
	cfg.FakeStreamEnabled = true
	provider := config.StaticProvider(cfg)
	cases := []struct {
		name, raw, want string
		fake, ok        bool
	}{
		{"基础模型", "enabled", "enabled", false, true},
		{"别名", "alias", "enabled", false, true},
		{"别名假流式", "fake-alias", "enabled", true, true},
		{"局部关闭假流式", "fake-no-fake", "no-fake", true, false},
		{"禁用模型", "disabled", "disabled", false, false},
		{"禁用模型别名", "off-alias", "disabled", false, false},
		{"未知模型", "missing", "missing", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, fake, ok := resolveConfiguredModel(tc.raw, provider)
			if got != tc.want || fake != tc.fake || ok != tc.ok {
				t.Fatalf("resolve(%q)=(%q,%v,%v), want (%q,%v,%v)", tc.raw, got, fake, ok, tc.want, tc.fake, tc.ok)
			}
		})
	}

	cfg.FakeStreamEnabled = false
	if got, fake, ok := resolveConfiguredModel("假流式-enabled", config.StaticProvider(cfg)); got != "enabled" || !fake || ok {
		t.Fatalf("全局关闭后 resolve=(%q,%v,%v)", got, fake, ok)
	}
}
