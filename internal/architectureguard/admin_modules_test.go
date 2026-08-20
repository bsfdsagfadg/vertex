package architectureguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminUsesModuleBoundaryWithoutInlineEvents(t *testing.T) {
	assets := filepath.Join("..", "admin", "assets")
	html, err := os.ReadFile(filepath.Join(assets, "admin.html"))
	if err != nil {
		t.Fatalf("read admin HTML: %v", err)
	}
	text := string(html)
	for _, forbidden := range []string{" onclick=", " onchange=", " oninput=", " onsubmit="} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Errorf("admin HTML contains inline event attribute %q", strings.TrimSpace(forbidden))
		}
	}
	if strings.Count(text, `<script type="module" src="admin.js"></script>`) != 1 {
		t.Error("admin HTML must have one ES module entrypoint")
	}
	for _, legacy := range []string{`<script src="utils.js">`, `<script src="api.js">`, `<script src="page-`} {
		if strings.Contains(text, legacy) {
			t.Errorf("admin HTML contains legacy classic script %q", legacy)
		}
	}
}

func TestAdminModulesDoNotRestoreGlobalBusinessState(t *testing.T) {
	assets := filepath.Join("..", "admin", "assets")
	for _, name := range []string{"admin.js", "page-settings.js", "page-models.js", "page-nodes.js", "page-appearance.js"} {
		data, err := os.ReadFile(filepath.Join(assets, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(data)
		for _, forbidden := range []string{
			"window.hasUnsavedSettings", "window.selectedNodeURIs", "window.selectedProxyURIs",
			"PAGE_CACHE", "window.resetColorTarget", "window.deletePreset",
		} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s contains global business state %q", name, forbidden)
			}
		}
	}
}
