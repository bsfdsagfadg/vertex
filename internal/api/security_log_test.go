package api

import "testing"

func TestRedactSubscriptionURL(t *testing.T) {
	got := redactSubscriptionURL("https://example.com/sub?token=secret#fragment")
	if got != "https://example.com/sub" {
		t.Fatalf("redacted URL=%q", got)
	}
}
