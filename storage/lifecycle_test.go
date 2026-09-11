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
