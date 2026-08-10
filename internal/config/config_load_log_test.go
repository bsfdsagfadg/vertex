package config

import "testing"

func TestShouldLogSuccessfulLoadOnlyWhenConfigChanges(t *testing.T) {
	mu.Lock()
	previousHash := lastLoadedConfigHash
	previousInitialized := hasLoadedConfigHash
	defer func() {
		lastLoadedConfigHash = previousHash
		hasLoadedConfigHash = previousInitialized
		mu.Unlock()
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
