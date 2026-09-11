package fetcher

import "testing"

func TestParseProxyPayloadSupportsDynamicSources(t *testing.T) {
	payload := []byte(`{
		"data":[{"ip":"1.2.3.4","port":"8080","protocols":["socks5"]}],
		"table_html":"<tr><td>5.6.7.8</td><td>1080</td></tr>",
		"extra":"9.10.11.12:3128"
	}`)
	proxies := parseProxyPayload(payload, "http")
	want := map[string]bool{"1.2.3.4:8080": true, "5.6.7.8:1080": true, "9.10.11.12:3128": true}
	if len(proxies) != len(want) {
		t.Fatalf("got %d proxies: %+v", len(proxies), proxies)
	}
	for _, proxy := range proxies {
		if !want[proxy.Address] {
			t.Fatalf("unexpected proxy: %+v", proxy)
		}
		if proxy.Address == "1.2.3.4:8080" && proxy.Protocol != "socks5" {
			t.Fatalf("JSON protocol metadata was ignored: %+v", proxy)
		}
	}
}

func TestParseProxyPayloadRejectsInvalidAddress(t *testing.T) {
	proxies := parseProxyPayload([]byte(`999.1.1.1:8080\n1.2.3.4:70000`), "http")
	if len(proxies) != 0 {
		t.Fatalf("expected no proxies, got %+v", proxies)
	}
}
