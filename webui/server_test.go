package webui

import (
	"net/url"
	"testing"

	"goproxy/storage"
)

func TestBuildProxyPageFiltersSortsAndBoundsRows(t *testing.T) {
	proxies := make([]storage.Proxy, 0, 140)
	for i := 0; i < 140; i++ {
		protocol := "http"
		country := "US New York"
		grade := "A"
		if i%2 == 0 {
			protocol = "socks5"
			country = "JP Tokyo"
			grade = "S"
		}
		proxies = append(proxies, storage.Proxy{
			Address: "proxy", Protocol: protocol, ExitLocation: country,
			QualityGrade: grade, Latency: 2000 - i, Source: "free",
		})
	}

	page := buildProxyPage(proxies, url.Values{
		"protocol":  {"socks5"},
		"country":   {"JP"},
		"page":      {"2"},
		"page_size": {"25"},
		"sort":      {"latency"},
	})
	if page.Total != 70 || page.Pages != 3 || page.Page != 2 {
		t.Fatalf("unexpected page metadata: %+v", page)
	}
	if len(page.Items) != 25 {
		t.Fatalf("expected 25 bounded rows, got %d", len(page.Items))
	}
	if len(page.Countries) != 1 || page.Countries[0] != "JP" {
		t.Fatalf("unexpected countries: %#v", page.Countries)
	}
	if page.Items[0].Latency > page.Items[len(page.Items)-1].Latency {
		t.Fatal("page is not sorted by latency")
	}
}

func TestBuildProxyPageCapsPageSizeAndSearches(t *testing.T) {
	proxies := []storage.Proxy{
		{Address: "1.2.3.4:80", Protocol: "http", ExitIP: "9.8.7.6", ExitLocation: "SG Singapore"},
		{Address: "5.6.7.8:80", Protocol: "http", ExitIP: "1.1.1.1", ExitLocation: "US Dallas"},
	}
	page := buildProxyPage(proxies, url.Values{"q": {"singapore"}, "page_size": {"1000"}})
	if page.PageSize != 100 || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("unexpected bounded search page: %+v", page)
	}
}
