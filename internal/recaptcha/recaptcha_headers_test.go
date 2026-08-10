package recaptcha

import "testing"

func TestEnterpriseJSHeadersUseFullXHRFingerprint(t *testing.T) {
	headers := enterpriseJSHeaders()
	for _, key := range []string{
		"sec-ch-ua",
		"sec-ch-ua-full-version",
		"x-goog-authuser",
		"x-browser-validation",
		"x-goog-ext-353267353-jspb",
		"origin",
		"referer",
		"sec-fetch-site",
	} {
		if len(headers[key]) == 0 {
			t.Fatalf("enterprise.js 缺少完整 XHR header %q", key)
		}
	}
	if got := headers["sec-fetch-site"][0]; got != "cross-site" {
		t.Fatalf("enterprise.js sec-fetch-site=%q, want cross-site", got)
	}
}
