package transport

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

func TestGetNextProxyCandidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("VPROXY_CONFIG", path)
	config.InvalidateCache()
	t.Cleanup(config.InvalidateCache)

	if got := GetNextProxyCandidate(); got != "" {
		t.Fatalf("空候选池应返回空串，got %q", got)
	}

	write := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		config.InvalidateCache()
	}
	cand := func(uri string, ok bool, testedAgo time.Duration) string {
		return fmt.Sprintf(`{"raw_uri":%q,"last_test_ok":%v,"last_test_at":%d,"name":"x"}`,
			uri, ok, time.Now().Add(-testedAgo).Unix())
	}

	// 未通过测速的候选同样参与轮询（配置即必用，LastTestOK 不再作筛选）
	write(`{"proxy_url_candidates":[` +
		cand("socks5://u1:p@h1:1080", true, time.Minute) + "," +
		cand("socks5://u2:p@h2:1080", false, time.Minute) +
		`]}`)
	seen := map[string]int{}
	for i := 0; i < 6; i++ {
		seen[GetNextProxyCandidate()]++
	}
	if seen["socks5://u1:p@h1:1080"] != 3 || seen["socks5://u2:p@h2:1080"] != 3 {
		t.Fatalf("启用候选应均匀轮询（不要求测速通过），got %v", seen)
	}

	// 多个启用候选 → Round-Robin 均匀轮询
	write(`{"proxy_url_candidates":[` +
		cand("socks5://u1:p@h1:1080", true, time.Minute) + "," +
		cand("socks5://u2:p@h2:1080", true, time.Minute) + "," +
		cand("socks5://u3:p@h3:1080", false, time.Minute) +
		`]}`)
	seen = map[string]int{}
	for i := 0; i < 6; i++ {
		seen[GetNextProxyCandidate()]++
	}
	if seen["socks5://u1:p@h1:1080"] != 2 || seen["socks5://u2:p@h2:1080"] != 2 || seen["socks5://u3:p@h3:1080"] != 2 {
		t.Fatalf("启用候选应均匀轮询，got %v", seen)
	}
}

func TestGetNextProxyCandidateIgnoresStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("VPROXY_CONFIG", path)
	config.InvalidateCache()
	t.Cleanup(config.InvalidateCache)

	write := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		config.InvalidateCache()
	}

	// 陈旧测速结果（>72h）的候选仍参与轮询（候选"配置即必用"，不剔除陈旧结果）
	write(`{"proxy_url_candidates":[
		{"raw_uri":"socks5://u1:p@h1:1080","last_test_ok":true,"last_test_at":1,"name":"A"},
		{"raw_uri":"socks5://u2:p@h2:1080","last_test_ok":true,"last_test_at":2,"name":"B"}
	]}`)
	seen := map[string]int{}
	for i := 0; i < 4; i++ {
		seen[GetNextProxyCandidate()]++
	}
	if seen["socks5://u1:p@h1:1080"] != 2 || seen["socks5://u2:p@h2:1080"] != 2 {
		t.Fatalf("陈旧候选仍应参与轮询，got %v", seen)
	}

	// 从未测试（无 last_test_at）的候选同样参与轮询
	write(`{"proxy_url_candidates":[
		{"raw_uri":"socks5://u1:p@h1:1080","name":"A"}
	]}`)
	for i := 0; i < 3; i++ {
		if got := GetNextProxyCandidate(); got != "socks5://u1:p@h1:1080" {
			t.Fatalf("从未测试的候选仍应参与轮询，got %q", got)
		}
	}
}

func TestGetNextProxyCandidateSkipsDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("VPROXY_CONFIG", path)
	config.InvalidateCache()
	t.Cleanup(config.InvalidateCache)

	body := fmt.Sprintf(`{"proxy_url_candidates":[
		{"raw_uri":"socks5://u1:p@h1:1080","last_test_ok":true,"last_test_at":%d,"name":"A","disabled":true},
		{"raw_uri":"socks5://u2:p@h2:1080","last_test_ok":true,"last_test_at":%d,"name":"B"}
	]}`,
		time.Now().Unix(), time.Now().Unix())
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	config.InvalidateCache()

	for i := 0; i < 5; i++ {
		if got := GetNextProxyCandidate(); got != "socks5://u2:p@h2:1080" {
			t.Fatalf("禁用候选不应被选中，应只返回启用的，got %q", got)
		}
	}

	// 全部禁用 → 候选池视为空，回退空串（调用方回退直连）
	body = fmt.Sprintf(`{"proxy_url_candidates":[
		{"raw_uri":"socks5://u1:p@h1:1080","last_test_ok":true,"last_test_at":%d,"name":"A","disabled":true}
	]}`, time.Now().Unix())
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	config.InvalidateCache()
	if got := GetNextProxyCandidate(); got != "" {
		t.Fatalf("全部禁用时应收敛为空串，got %q", got)
	}
}

func TestProxyCandidateNotAffectedByRequestFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("VPROXY_CONFIG", path)
	config.InvalidateCache()
	t.Cleanup(config.InvalidateCache)

	candURI := "socks5://u1:p@h1:1080"
	body := fmt.Sprintf(`{"proxy_url_candidates":[
		{"raw_uri":%q,"last_test_ok":true,"last_test_at":%d,"name":"A"}
	]}`, candURI, time.Now().Unix())
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	config.InvalidateCache()

	// 运行期熔断机制已废弃（v4）：链式请求中 node 段失败不归因入口，
	// 入口健康由 P9 独立拨测维护。因此无论"请求失败"多少次，候选仍应被继续选中。
	for i := 0; i < 10; i++ {
		if got := GetNextProxyCandidate(); got != candURI {
			t.Fatalf("请求失败不应影响候选选中（无运行期熔断），第 %d 次 got %q", i+1, got)
		}
	}

	// 候选池非空（存在启用候选）时，GetNextProxyCandidate 永不返回空串
	write := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		config.InvalidateCache()
	}
	write(`{"proxy_url_candidates":[
		{"raw_uri":"socks5://u1:p@h1:1080","last_test_ok":false,"last_test_at":1,"name":"A"},
		{"raw_uri":"socks5://u2:p@h2:1080","last_test_ok":true,"last_test_at":2,"name":"B","disabled":true}
	]}`)
	for i := 0; i < 5; i++ {
		if got := GetNextProxyCandidate(); got == "" {
			t.Fatalf("池内有启用候选时不应返回空串，第 %d 次", i+1)
		}
	}
}
