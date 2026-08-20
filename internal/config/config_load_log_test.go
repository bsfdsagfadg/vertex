package config

import "testing"

func TestDefaultConfigHasBoundedRequestSize(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxRequestMB != DefaultMaxRequestMB {
		t.Fatalf("default max request size=%d MB, want %d MB", cfg.MaxRequestMB, DefaultMaxRequestMB)
	}
}

func TestShouldLogSuccessfulLoadOnlyWhenConfigChanges(t *testing.T) {
	writeMu.Lock()
	previousHash := lastLoadedConfigHash
	previousInitialized := hasLoadedConfigHash
	defer func() {
		lastLoadedConfigHash = previousHash
		hasLoadedConfigHash = previousInitialized
		writeMu.Unlock()
	}()
	lastLoadedConfigHash = [32]byte{}
	hasLoadedConfigHash = false

	cfg := DefaultConfig()
	if !shouldLogSuccessfulLoad(cfg) {
		t.Fatal("first successful load should be logged")
	}
	if shouldLogSuccessfulLoad(cfg) {
		t.Fatal("unchanged configuration should not be logged again")
	}
	cfg.MaxRetries++
	if !shouldLogSuccessfulLoad(cfg) {
		t.Fatal("changed configuration should be logged")
	}
}
