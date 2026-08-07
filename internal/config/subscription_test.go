package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func setupSubscriptionConfigTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("VPROXY_CONFIG", filepath.Join(dir, "config.json"))
	subMu.Lock()
	globalSubConfig = SubscriptionConfig{}
	subscriptionsLoaded = false
	subMu.Unlock()
	t.Cleanup(func() {
		subMu.Lock()
		globalSubConfig = SubscriptionConfig{}
		subscriptionsLoaded = false
		subMu.Unlock()
	})
	return dir
}

func TestCustomUANameIsUniqueAndContentMayRepeat(t *testing.T) {
	setupSubscriptionConfigTest(t)
	if err := LoadSubscriptions(); err != nil {
		t.Fatal(err)
	}
	first, err := SaveCustomUA(CustomUA{Name: "Airport", UserAgent: "same-agent"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" {
		t.Fatal("custom UA must receive a stable ID")
	}
	if _, err = SaveCustomUA(CustomUA{Name: " airport ", UserAgent: "other-agent"}); !errors.Is(err, ErrCustomUANameConflict) {
		t.Fatalf("trimmed case-insensitive duplicate must conflict: %v", err)
	}
	second, err := SaveCustomUA(CustomUA{Name: "Backup", UserAgent: "same-agent"})
	if err != nil {
		t.Fatalf("different names may share UA content: %v", err)
	}
	first.Name = "Primary"
	if _, err = SaveCustomUA(first); err != nil {
		t.Fatalf("editing the same ID must not conflict: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("custom UA IDs must be unique")
	}
}

func TestCustomUAReferencesUseStableID(t *testing.T) {
	setupSubscriptionConfigTest(t)
	if err := LoadSubscriptions(); err != nil {
		t.Fatal(err)
	}
	ua, err := SaveCustomUA(CustomUA{Name: "Primary", UserAgent: "agent-a"})
	if err != nil {
		t.Fatal(err)
	}
	sub := Subscription{ID: "sub-a", Name: "A", URL: "https://example.com/sub", CustomUAID: ua.ID}
	if err = UpdateSubscription(sub); err != nil {
		t.Fatal(err)
	}
	if err = DeleteCustomUA(ua.ID); !errors.Is(err, ErrCustomUAInUse) {
		t.Fatalf("referenced custom UA must not be deleted: %v", err)
	}
	ua.UserAgent = "agent-b"
	if _, err = SaveCustomUA(ua); err != nil {
		t.Fatal(err)
	}
	stored, ok := GetSubscription(sub.ID)
	if !ok || stored.CustomUAID != ua.ID {
		t.Fatalf("editing UA must preserve subscription reference: %+v", stored)
	}
	resolved, err := ResolveSubscriptionUserAgent(stored)
	if err != nil || resolved != "agent-b" {
		t.Fatalf("subscription should resolve edited UA content, got %q, %v", resolved, err)
	}
	if stored.Revision < 2 {
		t.Fatalf("editing referenced UA must invalidate in-flight subscription updates: %+v", stored)
	}
}

func TestUnknownCustomUAReferencePreservesSentinel(t *testing.T) {
	setupSubscriptionConfigTest(t)
	if err := LoadSubscriptions(); err != nil {
		t.Fatal(err)
	}
	err := UpdateSubscription(Subscription{
		ID:         "sub-a",
		Name:       "A",
		URL:        "https://example.com/sub",
		CustomUAID: "ua_missing",
	})
	if !errors.Is(err, ErrCustomUANotFound) {
		t.Fatalf("unknown custom UA must preserve ErrCustomUANotFound: %v", err)
	}
}

func TestSubscriptionConfigReadsAreDeepCopies(t *testing.T) {
	setupSubscriptionConfigTest(t)
	if err := SaveSubscriptions(SubscriptionConfig{
		Subscriptions: []Subscription{{ID: "sub-a", Name: "A", URL: "https://example.com"}},
		CustomUAs:     []CustomUA{},
	}); err != nil {
		t.Fatal(err)
	}
	copyConfig := GetSubscriptionConfig()
	copyConfig.Subscriptions[0].Name = "mutated"
	stored := GetSubscriptionConfig()
	if stored.Subscriptions[0].Name != "A" {
		t.Fatalf("read must not expose global slice backing arrays: %+v", stored.Subscriptions[0])
	}
}

func TestConcurrentCustomUASavesDoNotOverwriteEachOther(t *testing.T) {
	setupSubscriptionConfigTest(t)
	if err := LoadSubscriptions(); err != nil {
		t.Fatal(err)
	}
	const count = 20
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := SaveCustomUA(CustomUA{Name: "ua-" + string(rune('a'+index)), UserAgent: "agent"})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := len(GetSubscriptionConfig().CustomUAs); got != count {
		t.Fatalf("concurrent saves lost updates: got %d want %d", got, count)
	}
}

func TestSubscriptionStatusRejectsStaleRevision(t *testing.T) {
	setupSubscriptionConfigTest(t)
	if err := LoadSubscriptions(); err != nil {
		t.Fatal(err)
	}
	sub := Subscription{ID: "sub-a", Name: "A", URL: "https://example.com", UserAgent: "Chrome"}
	if err := UpdateSubscription(sub); err != nil {
		t.Fatal(err)
	}
	first, _ := GetSubscription(sub.ID)
	first.URL = "https://example.com/edited"
	if err := UpdateSubscription(first); err != nil {
		t.Fatal(err)
	}
	updated, err := UpdateSubscriptionStatus(sub.ID, first.Revision, 123, "stale")
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("stale update result must be discarded")
	}
	stored, _ := GetSubscription(sub.ID)
	if stored.URL != first.URL || stored.LastError != "" {
		t.Fatalf("stale status overwrote edited subscription: %+v", stored)
	}
}

func TestLoadSubscriptionsMigratesMissingIDs(t *testing.T) {
	dir := setupSubscriptionConfigTest(t)
	legacy := SubscriptionConfig{
		Subscriptions: []Subscription{{ID: "sub-a", Name: "A", URL: "https://example.com"}},
		CustomUAs:     []CustomUA{{Name: "Legacy", UserAgent: "agent"}},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "subscriptions.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err = LoadSubscriptions(); err != nil {
		t.Fatal(err)
	}
	loaded := GetSubscriptionConfig()
	if loaded.CustomUAs[0].ID == "" || loaded.Subscriptions[0].Revision == 0 {
		t.Fatalf("legacy config IDs/revisions were not migrated: %+v", loaded)
	}
}
