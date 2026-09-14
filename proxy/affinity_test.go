package proxy

import (
	"errors"
	"path/filepath"
	"testing"

	"goproxy/config"
	"goproxy/storage"
)

func selectorFrom(proxies ...storage.Proxy) ProxySelector {
	return func(excludes []string) (*storage.Proxy, error) {
		excluded := map[string]bool{}
		for _, address := range excludes {
			excluded[address] = true
		}
		for _, candidate := range proxies {
			if !excluded[candidate.Address] {
				copy := candidate
				return &copy, nil
			}
		}
		return nil, errors.New("no proxy")
	}
}

func TestAffinityReusesSuccessfulLease(t *testing.T) {
	m := NewAffinityManager()
	selector := selectorFrom(storage.Proxy{Address: "one", Protocol: "http"})
	first, err := m.Select("aid:1", "bilibili", "http", nil, selector)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Select("aid:1", "bilibili", "http", nil, selector)
	if err != nil {
		t.Fatal(err)
	}
	if first.Address != second.Address {
		t.Fatalf("expected sticky lease, got %q then %q", first.Address, second.Address)
	}
	if !m.Feedback("aid:1", true, "") {
		t.Fatal("expected feedback to match active lease")
	}
}

func TestRiskFailureForcesDifferentProxy(t *testing.T) {
	m := NewAffinityManager()
	selector := selectorFrom(
		storage.Proxy{Address: "one", Protocol: "http"},
		storage.Proxy{Address: "two", Protocol: "http"},
	)
	first, err := m.Select("aid:2", "bilibili", "http", nil, selector)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Feedback("aid:2", false, "HTTP Error 412: Precondition Failed") {
		t.Fatal("expected feedback to match active lease")
	}
	second, err := m.Select("aid:2", "bilibili", "http", nil, selector)
	if err != nil {
		t.Fatal(err)
	}
	if first.Address == second.Address {
		t.Fatal("risk failure reused the cooling proxy")
	}
	status := m.Status()
	if status.RiskFailures != 1 || status.CoolingProxies != 1 {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestDifferentSessionsDoNotShareLease(t *testing.T) {
	m := NewAffinityManager()
	selector := selectorFrom(
		storage.Proxy{Address: "one", Protocol: "http"},
		storage.Proxy{Address: "two", Protocol: "http"},
	)
	first, err := m.Select("aid:3", "bilibili", "http", nil, selector)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Select("aid:4", "bilibili", "http", nil, selector)
	if err != nil {
		t.Fatal(err)
	}
	if first.Address == second.Address {
		t.Fatal("concurrent sessions shared one upstream")
	}
}

func TestSplitSessionUsername(t *testing.T) {
	session, ok := splitSessionUsername("topic~aid:99", "topic")
	if !ok || session != "aid:99" {
		t.Fatalf("unexpected session parse: %q %v", session, ok)
	}
	if _, ok := splitSessionUsername("other~aid:99", "topic"); ok {
		t.Fatal("accepted a different base username")
	}
}

func TestApplicationAndTransportMetricsAreSeparated(t *testing.T) {
	m := NewAffinityManager()
	selector := selectorFrom(storage.Proxy{Address: "one", Protocol: "http"})
	if _, err := m.Select("aid:metrics", "bilibili", "http", nil, selector); err != nil {
		t.Fatal(err)
	}
	m.RecordTransport("aid:metrics", "one", true, "")
	m.Feedback("aid:metrics", false, "HTTP 412")
	status := m.Status()
	if status.TransportAttempts != 1 || status.TransportSuccessRate != 100 {
		t.Fatalf("unexpected transport metrics: %+v", status)
	}
	if status.CollectionRequests != 1 || status.CollectionSuccessRate != 0 || status.RiskRate != 100 {
		t.Fatalf("unexpected application metrics: %+v", status)
	}
}

func TestFeedbackWithoutLiveLeaseStillCountsCollectionResult(t *testing.T) {
	m := NewAffinityManager()
	if m.Feedback("aid:no-lease", false, "proxy connection failed") {
		t.Fatal("unexpected lease match")
	}
	status := m.Status()
	if status.CollectionRequests != 1 || status.CollectionFailures != 1 {
		t.Fatalf("unmatched feedback was not counted: %+v", status)
	}
}

func TestHTTPServingPortNeverSelectsSOCKSUpstream(t *testing.T) {
	store, err := storage.New(filepath.Join(t.TempDir(), "proxy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.AddProxy("1.1.1.1:1080", "socks5"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddProxy("2.2.2.2:8080", "http"); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.CustomPriority = false
	cfg.CustomFreePriority = false
	server := New(store, cfg, "random", ":7777")
	for i := 0; i < 20; i++ {
		selected, err := server.selectProxyFromStorage(nil, false, "", cfg)
		if err != nil {
			t.Fatal(err)
		}
		if selected.Protocol != "http" {
			t.Fatalf("HTTP port selected %s upstream: %+v", selected.Protocol, selected)
		}
	}
}
