package transport

import (
	"crypto/sha1"
	"encoding/base64"
	"strings"

	http "github.com/bogdanfinn/fhttp"
)

// Chrome 150 的 UA 与 sec-ch-ua（与 transport 的 TLS 指纹保持一致）。
const (
	userAgent           = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"
	chUA                = `"Not;A=Brand";v="8", "Chromium";v="150", "Google Chrome";v="150"`
	chUAFullVersionList = `"Not;A=Brand";v="8.0.0.0", "Chromium";v="150.0.7871.13", "Google Chrome";v="150.0.7871.13"`
	chUAArch            = `"x86"`
	chUABitness         = `"64"`
	chUAFullVersion     = `"150.0.7871.13"`
	chUAPlatformVersion = `"19.0.0"`
	chUAModel           = `""`
	chUAWow64           = `?0`
	chUAFormFactors     = `"Desktop"`
)

// XHRHeaders 构造 XHR/fetch 风格的完整 Chrome 请求头（含 H2 头顺序 HeaderOrderKey）。
//
// tls-client 不像 curl_cffi 那样自动补 sec-ch-ua / sec-fetch-* 等头，必须手动设全，
// 否则 Google 匿名端点的指纹校验不通过。逐字节抄自经实测的 PoC（recaptcha reload
// 与 batchGraphql 都用此头）。
func XHRHeaders(contentType, accept, origin, referer, site string) http.Header {
	h := http.Header{
		"sec-ch-ua":                   {chUA},
		"sec-ch-ua-mobile":            {"?0"},
		"sec-ch-ua-platform":          {`"Windows"`},
		"sec-ch-ua-arch":              {chUAArch},
		"sec-ch-ua-bitness":           {chUABitness},
		"sec-ch-ua-full-version":      {chUAFullVersion},
		"sec-ch-ua-full-version-list": {chUAFullVersionList},
		"sec-ch-ua-platform-version":  {chUAPlatformVersion},
		"sec-ch-ua-model":             {chUAModel},
		"sec-ch-ua-wow64":             {chUAWow64},
		"sec-ch-ua-form-factors":      {chUAFormFactors},
		"user-agent":                  {userAgent},
		"accept":                      {accept},
		"origin":                      {origin},
		"sec-fetch-site":              {site},
		"sec-fetch-mode":              {"cors"},
		"sec-fetch-dest":              {"empty"},
		"referer":                     {referer},
		"accept-encoding":             {"gzip, deflate, br"},
		"accept-language":             {"zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6"},
		"priority":                    {"u=1, i"},
		"x-goog-authuser":             {"0"},
		"x-browser-channel":           {"stable"},
		"x-browser-copyright":         {"Copyright 2026 Google LLC. All Rights Reserved."},
		"x-browser-validation":        {GenerateXBrowserValidation(userAgent)},
		"x-browser-year":              {"2026"},
		"x-goog-ext-353267353-jspb":   {"[null,null,null,194274]"},
		http.HeaderOrderKey: {
			"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "sec-ch-ua-arch", "sec-ch-ua-bitness",
			"sec-ch-ua-full-version", "sec-ch-ua-full-version-list", "sec-ch-ua-platform-version",
			"sec-ch-ua-model", "sec-ch-ua-wow64", "sec-ch-ua-form-factors",
			"user-agent", "content-type", "accept", "x-goog-authuser", "x-browser-channel",
			"x-browser-copyright", "x-browser-validation", "x-browser-year", "x-goog-ext-353267353-jspb",
			"origin", "sec-fetch-site", "sec-fetch-mode", "sec-fetch-dest",
			"referer", "accept-encoding", "accept-language", "priority",
		},
	}
	if contentType != "" {
		h["content-type"] = []string{contentType}
	}
	return h
}

// AnchorHeaders 构造 recaptcha anchor iframe 导航请求头。逐字节抄自 PoC。
func AnchorHeaders() http.Header {
	return http.Header{
		"sec-ch-ua":                   {chUA},
		"sec-ch-ua-mobile":            {"?0"},
		"sec-ch-ua-platform":          {`"Windows"`},
		"sec-ch-ua-arch":              {chUAArch},
		"sec-ch-ua-bitness":           {chUABitness},
		"sec-ch-ua-full-version":      {chUAFullVersion},
		"sec-ch-ua-full-version-list": {chUAFullVersionList},
		"sec-ch-ua-platform-version":  {chUAPlatformVersion},
		"sec-ch-ua-model":             {chUAModel},
		"sec-ch-ua-wow64":             {chUAWow64},
		"sec-ch-ua-form-factors":      {chUAFormFactors},
		"upgrade-insecure-requests":   {"1"},
		"user-agent":                  {userAgent},
		"accept":                      {"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"},
		"sec-fetch-site":              {"cross-site"},
		"sec-fetch-mode":              {"navigate"},
		"sec-fetch-dest":              {"iframe"},
		"accept-encoding":             {"gzip, deflate, br"},
		"accept-language":             {"zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6"},
		http.HeaderOrderKey: {
			"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "sec-ch-ua-arch", "sec-ch-ua-bitness",
			"sec-ch-ua-full-version", "sec-ch-ua-full-version-list", "sec-ch-ua-platform-version",
			"sec-ch-ua-model", "sec-ch-ua-wow64", "sec-ch-ua-form-factors",
			"upgrade-insecure-requests",
			"user-agent", "accept", "sec-fetch-site", "sec-fetch-mode", "sec-fetch-dest",
			"accept-encoding", "accept-language",
		},
	}
}

// GenerateXBrowserValidation calculates the x-browser-validation header based on User-Agent.
func GenerateXBrowserValidation(ua string) string {
	apiKey := "AIzaSyA2KlwBX3mkFo30om9LUFYQhpqLoa_BNhE" // Default Windows key
	uaLower := strings.ToLower(ua)
	if strings.Contains(uaLower, "linux") {
		apiKey = "AIzaSyBqJZh-7pA44blAaAkH6490hUFOwX0KCYM"
	} else if strings.Contains(uaLower, "macintosh") || strings.Contains(uaLower, "mac os x") {
		apiKey = "AIzaSyDr2UxVnv_U85AbhhY8XSHSIavUW0DC-sY"
	}
	h := sha1.New()
	h.Write([]byte(apiKey + ua))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
