package repository_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/db"
	"github.com/bsfdsagfadg/vertex/internal/domain"
	"github.com/bsfdsagfadg/vertex/internal/repository"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_data.db")
	db.CloseDB()
	if err := db.InitDB(dbPath); err != nil {
		t.Fatalf("failed to init test db: %v", err)
	}
	t.Cleanup(func() {
		db.CloseDB()
	})
}

func TestSQLiteNodeRepository_CRUD(t *testing.T) {
	setupTestDB(t)
	repo := repository.NewSQLiteNodeRepository(db.GlobalDBX)
	ctx := context.Background()

	testNodes := []domain.Node{
		{Type: "vless", Name: "Node 1", RawURI: "vless://user1@server1.com:443#Node 1", Disabled: false},
		{Type: "vmess", Name: "Node 2", RawURI: "vmess://user2@server2.com:443#Node 2", Disabled: true},
	}

	err := repo.UpsertNodesWithSource(ctx, testNodes, domain.NodeSource{Type: domain.SourceManual}, false)
	if err != nil {
		t.Fatalf("UpsertNodesWithSource failed: %v", err)
	}

	nodes, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}

	sources, err := repo.GetSources(ctx, testNodes[0].RawURI)
	if err != nil {
		t.Fatalf("GetSources failed: %v", err)
	}
	if len(sources) != 1 || sources[0].Type != domain.SourceManual {
		t.Fatalf("unexpected sources: %+v", sources)
	}

	// Test replace subscription nodes
	subNodes := []domain.Node{
		{Type: "vless", Name: "Sub Node 1", RawURI: "vless://user1@server1.com:443#Node 1", Disabled: false},
		{Type: "trojan", Name: "Sub Node 3", RawURI: "trojan://pass@server3.com:443#Sub Node 3", Disabled: false},
	}
	removed, err := repo.ReplaceSubscriptionNodes(ctx, "sub-1", subNodes, false)
	if err != nil {
		t.Fatalf("ReplaceSubscriptionNodes failed: %v", err)
	}
	_ = removed

	allNodes, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll after sub replace failed: %v", err)
	}
	if len(allNodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(allNodes))
	}

	// Test batch set disabled
	err = repo.BatchSetDisabled(ctx, []string{"trojan://pass@server3.com:443#Sub Node 3"}, true)
	if err != nil {
		t.Fatalf("BatchSetDisabled failed: %v", err)
	}
	n3, err := repo.GetByURI(ctx, "trojan://pass@server3.com:443#Sub Node 3")
	if err != nil || n3 == nil || !n3.Disabled {
		t.Fatalf("expected n3 to be disabled, got %+v (err: %v)", n3, err)
	}

	// Test delete disabled
	disabledRemoved, err := repo.DeleteDisabled(ctx)
	if err != nil {
		t.Fatalf("DeleteDisabled failed: %v", err)
	}
	if len(disabledRemoved) != 2 { // Node 2 and Sub Node 3
		t.Fatalf("expected 2 disabled nodes removed, got %d", len(disabledRemoved))
	}
}

func TestSQLiteHealthRepository_ReliableQueue(t *testing.T) {
	setupTestDB(t)
	repo := repository.NewSQLiteHealthRepository(db.GlobalDBX)
	defer repo.Close()
	ctx := context.Background()

	rawURI := "vless://user@server.com:443#Node 1"
	nodeRepo := repository.NewSQLiteNodeRepository(db.GlobalDBX)
	_ = nodeRepo.UpsertNodesWithSource(ctx, []domain.Node{{Type: "vless", Name: "Node 1", RawURI: rawURI}}, domain.NodeSource{Type: domain.SourceManual}, false)
	repo.RecordTest(rawURI, true, 120.5, "")
	h, err := repo.GetByURI(ctx, rawURI)
	if err != nil || h == nil {
		t.Fatalf("GetByURI failed: %v, h: %+v", err, h)
	}
	if h.SuccessCount != 1 || h.LastTestMs != 120.5 {
		t.Fatalf("unexpected health in memory: %+v", h)
	}

	repo.RecordRateLimit(rawURI, 60)
	h, err = repo.GetByURI(ctx, rawURI)
	if err != nil || h == nil {
		t.Fatalf("GetByURI after 429 failed: %v", err)
	}
	if h.RateLimitCount != 1 || h.CooldownUntil <= time.Now().Unix() {
		t.Fatalf("expected cooldown set, got %+v", h)
	}

	if err := repo.Flush(ctx); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// Read directly from DB to verify persistence
	freshRepo := repository.NewSQLiteHealthRepository(db.GlobalDBX)
	defer freshRepo.Close()
	freshH, err := freshRepo.GetByURI(ctx, rawURI)
	if err != nil || freshH == nil {
		t.Fatalf("freshRepo GetByURI failed: %v", err)
	}
	if freshH.RateLimitCount != 1 || freshH.SuccessCount != 1 {
		t.Fatalf("persisted health mismatched: %+v", freshH)
	}
}

func TestSQLiteSubscriptionRepository(t *testing.T) {
	setupTestDB(t)
	repo := repository.NewSQLiteSubscriptionRepository(db.GlobalDBX)
	ctx := context.Background()

	sub := domain.Subscription{
		ID:             "sub-test-1",
		Name:           "Test Subscription",
		URL:            "https://example.com/sub",
		UserAgent:      "Clash.Meta",
		UpdateInterval: 60,
		AdoptManual:    true,
	}

	if err := repo.Save(ctx, sub); err != nil {
		t.Fatalf("Save subscription failed: %v", err)
	}

	got, err := repo.GetByID(ctx, sub.ID)
	if err != nil || got == nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if got.Name != sub.Name || got.AdoptManual != true {
		t.Fatalf("mismatched subscription: %+v", got)
	}

	// Custom UA
	ua := domain.CustomUA{
		ID:        "ua-1",
		Name:      "Custom Verge",
		UserAgent: "clash-verge/v2.5.2",
	}
	if err := repo.SaveCustomUA(ctx, ua); err != nil {
		t.Fatalf("SaveCustomUA failed: %v", err)
	}
	gotUA, err := repo.GetCustomUAByID(ctx, ua.ID)
	if err != nil || gotUA == nil || gotUA.UserAgent != ua.UserAgent {
		t.Fatalf("mismatched custom ua: %+v", gotUA)
	}
}

func TestSQLiteEntryProxyRepository(t *testing.T) {
	setupTestDB(t)
	repo := repository.NewSQLiteEntryProxyRepository(db.GlobalDBX)
	ctx := context.Background()

	cand := domain.EntryProxyCandidate{
		RawURI:        "socks5://127.0.0.1:1080#LocalSocks",
		NormalizedURI: "socks5://127.0.0.1:1080",
		Name:          "LocalSocks",
		Type:          "socks5",
	}

	if err := repo.Add(ctx, cand); err != nil {
		t.Fatalf("Add entry proxy failed: %v", err)
	}

	exists, err := repo.Exists(ctx, cand.NormalizedURI)
	if err != nil || !exists {
		t.Fatalf("Exists expected true, got %v (err: %v)", exists, err)
	}

	// Test update probe failure with auto disable
	for i := range 3 {
		autoDisabled, err := repo.UpdateTestResult(ctx, cand.NormalizedURI, false, 0, "timeout", 60, true, true, 3)
		if err != nil {
			t.Fatalf("UpdateTestResult failed: %v", err)
		}
		if i == 2 && !autoDisabled {
			t.Fatalf("expected candidate to be auto-disabled on 3rd failure")
		}
	}

	updated, err := repo.GetByNormalizedURI(ctx, cand.NormalizedURI)
	if err != nil || updated == nil || !updated.Disabled {
		t.Fatalf("expected candidate to be disabled: %+v", updated)
	}
}
