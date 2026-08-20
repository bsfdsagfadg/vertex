package api

import (
	"bytes"
	"context"
	"errors"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/repository"
)

func setupEntryProxyProbeTest(t *testing.T, name string) {
	t.Helper()
	repo, err := repository.Open(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	config.SetRepository(repo)
	t.Cleanup(func() {
		config.SetRepository(nil)
		_ = repo.Close()
	})
}

func TestEntryProxyProbeScheduleUsesUpdatedInterval(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	var schedule entryProxyProbeSchedule
	if schedule.due(now, true, 5*time.Minute) {
		t.Fatal("new schedule should wait for its interval")
	}
	if schedule.due(now.Add(4*time.Minute), true, 5*time.Minute) {
		t.Fatal("schedule fired early")
	}
	if !schedule.due(now.Add(5*time.Minute), true, 5*time.Minute) {
		t.Fatal("schedule did not fire at configured interval")
	}
	schedule.completed(now.Add(5 * time.Minute))
	if schedule.due(now.Add(6*time.Minute), true, 2*time.Minute) {
		t.Fatal("changed interval should restart the countdown")
	}
	if !schedule.due(now.Add(8*time.Minute), true, 2*time.Minute) {
		t.Fatal("updated interval was not applied")
	}
	if schedule.due(now.Add(9*time.Minute), false, 2*time.Minute) || !schedule.next.IsZero() {
		t.Fatal("disabling probes did not clear the schedule")
	}
}

func TestEntryProxyProbeRoundLogsFailuresAndSummary(t *testing.T) {
	setupEntryProxyProbeTest(t, "entry-probe.db")

	successURI := "socks5://127.0.0.1:1280#success"
	failureURI := "socks5://127.0.0.1:1281#failure"
	for _, uri := range []string{successURI, failureURI} {
		if _, err := config.AddProxyCandidate(uri); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.DefaultConfig()
	cfg.EntryProxyProbeEnabled = true
	cfg.EntryProxyProbeCooldownSeconds = 90
	provider := config.StaticProvider(cfg)

	var logs bytes.Buffer
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	})

	summary := runEntryProxyProbeRound(context.Background(), provider, func(_ context.Context, rawURI string) (float64, error) {
		if rawURI == failureURI {
			return 25, errors.New("i/o timeout")
		}
		return 12, nil
	})

	if summary.Total != 2 || summary.Success != 1 || summary.Failed != 1 || summary.Cooling != 1 || summary.AutoDisabled != 0 {
		t.Fatalf("unexpected probe summary: %+v", summary)
	}
	output := logs.String()
	for _, expected := range []string{
		"[EntryProxy] 自动拨测开始: 2 个节点测试",
		"周期拨测失败: i/o timeout",
		"[EntryProxy] 自动拨测结束: 2 个节点自动测试完毕，1 个成功，1 个失败，1 个冷却，0 个自动禁用",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("missing log %q in:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "周期拨测成功") {
		t.Fatalf("normal mode logged successful candidate:\n%s", output)
	}
}

func TestEntryProxyProbeRoundLogsSuccessInDebugMode(t *testing.T) {
	setupEntryProxyProbeTest(t, "entry-probe-debug.db")
	uri := "socks5://127.0.0.1:1282#debug"
	if _, err := config.AddProxyCandidate(uri); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.DebugMode = true

	var logs bytes.Buffer
	oldWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldWriter) })
	runEntryProxyProbeRound(context.Background(), config.StaticProvider(cfg), func(context.Context, string) (float64, error) {
		return 18, nil
	})
	if !strings.Contains(logs.String(), "周期拨测成功: 18ms") {
		t.Fatalf("debug mode did not log successful probe: %s", logs.String())
	}
}

func TestEntryProxyProbeRoundReportsAutoDisableSeparatelyFromCooldown(t *testing.T) {
	setupEntryProxyProbeTest(t, "entry-probe-disable.db")
	uri := "socks5://127.0.0.1:1283#disable"
	if _, err := config.AddProxyCandidate(uri); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.EntryProxyProbeCooldownSeconds = 60
	cfg.EntryProxyProbeAutoDisableEnabled = true
	cfg.EntryProxyProbeAutoDisableFailures = 1

	var logs bytes.Buffer
	oldWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldWriter) })
	summary := runEntryProxyProbeRound(context.Background(), config.StaticProvider(cfg), func(context.Context, string) (float64, error) {
		return 20, errors.New("timeout")
	})
	if summary.Failed != 1 || summary.AutoDisabled != 1 || summary.Cooling != 0 {
		t.Fatalf("unexpected auto-disable summary: %+v", summary)
	}
	if !strings.Contains(logs.String(), "连续失败 1 次，已自动禁用") {
		t.Fatalf("auto-disable failure log missing: %s", logs.String())
	}
}
