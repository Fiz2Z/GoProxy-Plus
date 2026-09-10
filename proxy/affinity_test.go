package proxy

import (
	"errors"
	"testing"

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

func TestShouldPenalizeApplicationResponse(t *testing.T) {
	tests := map[int]bool{
		200: false,
		404: false,
		408: true,
		412: true,
		429: true,
		500: true,
	}
	for status, want := range tests {
		if got := shouldPenalizeApplicationResponse(status); got != want {
			t.Fatalf("status %d: got %v, want %v", status, got, want)
		}
	}
}
