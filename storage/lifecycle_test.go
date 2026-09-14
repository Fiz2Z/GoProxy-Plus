package storage

import (
	"path/filepath"
	"testing"
)

func TestProxySelectionWeightPrefersHighGrades(t *testing.T) {
	s := proxySelectionWeight(Proxy{QualityGrade: "S"})
	a := proxySelectionWeight(Proxy{QualityGrade: "A"})
	b := proxySelectionWeight(Proxy{QualityGrade: "B"})
	c := proxySelectionWeight(Proxy{QualityGrade: "C"})
	if !(s > a && a > b && b > c) {
		t.Fatalf("unexpected weights S=%d A=%d B=%d C=%d", s, a, b, c)
	}
	if proxySelectionWeight(Proxy{QualityGrade: "S", FailCount: 2}) >= s {
		t.Fatal("consecutive failures did not reduce selection weight")
	}
}

func TestAddProxyIfNewDistinguishesDuplicates(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "proxy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()

	inserted, err := store.AddProxyIfNew("9.9.9.9:8080", "http")
	if err != nil || !inserted {
		t.Fatalf("first insert: inserted=%v err=%v", inserted, err)
	}
	inserted, err = store.AddProxyIfNew("9.9.9.9:8080", "http")
	if err != nil || inserted {
		t.Fatalf("duplicate insert: inserted=%v err=%v", inserted, err)
	}
}

func TestFailureLifecycleDisablesThenExpires(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "proxy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	if err := store.AddProxy("1.2.3.4:8080", "http"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if err := store.MarkProxyFailure("1.2.3.4:8080", true, 3); err != nil {
			t.Fatal(err)
		}
		var status string
		var failures int
		if err := store.db.QueryRow(`SELECT status, fail_count FROM proxies WHERE address=?`, "1.2.3.4:8080").Scan(&status, &failures); err != nil {
			t.Fatal(err)
		}
		if i < 3 && status != "degraded" {
			t.Fatalf("failure %d unexpectedly set status %s", i, status)
		}
		if i == 3 && status != "disabled" {
			t.Fatalf("third failure did not disable proxy: %s", status)
		}
	}
	store.db.Exec(`UPDATE proxies SET failure_since=datetime('now','-25 hours') WHERE address=?`, "1.2.3.4:8080")
	deleted, err := store.DeleteExpiredFailed(24)
	if err != nil || deleted != 1 {
		t.Fatalf("delete expired failed proxy: deleted=%d err=%v", deleted, err)
	}
}

func TestWeightedRandomUsesHighQualityTierFirst(t *testing.T) {
	proxies := []Proxy{
		{Address: "slow", QualityGrade: "C"},
		{Address: "fallback", QualityGrade: "B"},
		{Address: "fast", QualityGrade: "A"},
	}
	for i := 0; i < 100; i++ {
		if got := weightedRandomProxy(proxies); got.Address != "fast" {
			t.Fatalf("selected lower tier while A grade was available: %+v", got)
		}
	}
}

func TestTransportSuccessDoesNotEraseApplicationFailures(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "proxy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	const address = "2.3.4.5:8080"
	if err := store.AddProxy(address, "http"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if err := store.RecordProxyUse(address, true); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordApplicationResult(address, false, false, 3); err != nil {
			t.Fatal(err)
		}
	}
	var status string
	var failures int
	if err := store.db.QueryRow(`SELECT status, fail_count FROM proxies WHERE address=?`, address).Scan(&status, &failures); err != nil {
		t.Fatal(err)
	}
	if status != "disabled" || failures != 3 {
		t.Fatalf("application failures were erased by transport success: status=%s failures=%d", status, failures)
	}
}

func TestRiskFeedbackDoesNotDisableOtherwiseHealthyProxy(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "proxy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	const address = "3.4.5.6:8080"
	if err := store.AddProxy(address, "http"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := store.RecordApplicationResult(address, false, true, 3); err != nil {
			t.Fatal(err)
		}
	}
	var status string
	var failures int
	if err := store.db.QueryRow(`SELECT status, fail_count FROM proxies WHERE address=?`, address).Scan(&status, &failures); err != nil {
		t.Fatal(err)
	}
	if status != "active" || failures != 0 {
		t.Fatalf("risk feedback changed durable health: status=%s failures=%d", status, failures)
	}
}

func TestMissingExitInfoIsQuarantinedNotDeleted(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "proxy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	if err := store.AddProxy("5.6.7.8:3128", "http"); err != nil {
		t.Fatal(err)
	}
	changed, err := store.QuarantineWithoutExitInfo()
	if err != nil || changed != 1 {
		t.Fatalf("quarantine: changed=%d err=%v", changed, err)
	}
	var status string
	if err := store.db.QueryRow(`SELECT status FROM proxies WHERE address=?`, "5.6.7.8:3128").Scan(&status); err != nil {
		t.Fatalf("proxy was deleted: %v", err)
	}
	if status != "disabled" {
		t.Fatalf("expected disabled proxy, got %s", status)
	}
}
