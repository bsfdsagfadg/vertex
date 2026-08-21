package transport

import (
	"testing"
)

func TestXHRHeaders(t *testing.T) {
	h := XHRHeaders("application/json", "application/json", "https://example.com", "https://example.com/referer", "same-origin")

	if h.Get("content-type") != "application/json" && len(h["content-type"]) > 0 && h["content-type"][0] != "application/json" {
		t.Errorf("Expected content-type application/json, got %s", h["content-type"])
	}
	if h.Get("accept") != "application/json" && (len(h["accept"]) == 0 || h["accept"][0] != "application/json") {
		t.Errorf("Expected accept application/json, got %s", h["accept"])
	}
	if h.Get("origin") != "https://example.com" && (len(h["origin"]) == 0 || h["origin"][0] != "https://example.com") {
		t.Errorf("Expected origin https://example.com, got %s", h["origin"])
	}
	if h.Get("referer") != "https://example.com/referer" && (len(h["referer"]) == 0 || h["referer"][0] != "https://example.com/referer") {
		t.Errorf("Expected referer https://example.com/referer, got %s", h["referer"])
	}
	if h.Get("sec-fetch-site") != "same-origin" && (len(h["sec-fetch-site"]) == 0 || h["sec-fetch-site"][0] != "same-origin") {
		t.Errorf("Expected sec-fetch-site same-origin, got %s", h["sec-fetch-site"])
	}
	if h.Get("user-agent") == "" && len(h["user-agent"]) == 0 {
		t.Errorf("Expected user-agent to be set")
	}

	// Test without content-type
	h2 := XHRHeaders("", "*/*", "", "", "")
	if len(h2["content-type"]) != 0 {
		t.Errorf("Expected no content-type, got %v", h2["content-type"])
	}
}

func TestAnchorHeaders(t *testing.T) {
	h := AnchorHeaders()
	if h.Get("sec-fetch-dest") != "iframe" && (len(h["sec-fetch-dest"]) == 0 || h["sec-fetch-dest"][0] != "iframe") {
		t.Errorf("Expected sec-fetch-dest iframe, got %s", h["sec-fetch-dest"])
	}
	if h.Get("user-agent") == "" && len(h["user-agent"]) == 0 {
		t.Errorf("Expected user-agent to be set")
	}
	if h.Get("sec-fetch-site") != "cross-site" && (len(h["sec-fetch-site"]) == 0 || h["sec-fetch-site"][0] != "cross-site") {
		t.Errorf("Expected sec-fetch-site cross-site, got %s", h["sec-fetch-site"])
	}
}
