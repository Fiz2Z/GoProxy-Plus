package fetcher

import (
	"path/filepath"
	"testing"

	"goproxy/storage"
)

func TestSourceManagerCoolsLowQualitySourcesAndReportsFunnel(t *testing.T) {
	store, err := storage.New(filepath.Join(t.TempDir(), "source-quality.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	manager := NewSourceManager(store.GetDB())
	url := "https://example.test/proxies.txt"
	manager.RecordSuccess(url)
	manager.RecordValidationBatch(url, ValidationStats{
		Candidates:  100,
		BaseSuccess: 8,
		TLSSuccess:  0,
		ExitSuccess: 0,
		Admitted:    0,
	})

	if manager.CanUseSource(url) {
		t.Fatal("expected a low-quality source to enter cooldown")
	}

	stats, err := manager.GetSourceStats()
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected one source, got %d", len(stats))
	}
	got := stats[0]
	if got["last_candidates"] != 100 || got["last_base"] != 8 || got["last_tls"] != 0 {
		t.Fatalf("unexpected validation funnel: %#v", got)
	}
	if got["quality_status"] != "degraded" {
		t.Fatalf("expected degraded quality status, got %v", got["quality_status"])
	}

	manager.RecordValidationBatch(url, ValidationStats{Candidates: 100})
	manager.RecordValidationBatch(url, ValidationStats{Candidates: 100})
	stats, err = manager.GetSourceStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats[0]["quality_status"] != "disabled" {
		t.Fatalf("expected disabled after three low-quality batches, got %v", stats[0]["quality_status"])
	}
}

func TestSourceManagerKeepsUsefulSourceActive(t *testing.T) {
	store, err := storage.New(filepath.Join(t.TempDir(), "source-active.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	manager := NewSourceManager(store.GetDB())
	url := "https://example.test/fresh.txt"
	manager.RecordValidationBatch(url, ValidationStats{
		Candidates:  200,
		BaseSuccess: 40,
		TLSSuccess:  10,
		ExitSuccess: 9,
		Admitted:    8,
	})
	if !manager.CanUseSource(url) {
		t.Fatal("expected a useful source to remain active")
	}
}
