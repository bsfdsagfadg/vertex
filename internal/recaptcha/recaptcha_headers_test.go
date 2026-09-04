package recaptcha

import "testing"

func TestEnterpriseJSHeadersUseNativeScriptFingerprint(t *testing.T) {
	headers := enterpriseJSHeaders()
	for _, key := range []string{
		"user-agent", "accept", "accept-language",
	} {
		if len(headers[key]) == 0 {
			t.Fatalf("enterprise.js 缺少原生脚本 header %q", key)
		}
	}
	for _, key := range []string{"x-goog-authuser", "x-browser-validation", "x-goog-ext-353267353-jspb", "origin", "referer", "sec-fetch-site"} {
		if len(headers[key]) != 0 {
			t.Fatalf("enterprise.js 不应带 XHR 私有 header %q", key)
		}
	}
}
