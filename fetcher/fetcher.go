package fetcher

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"goproxy/storage"
)

// 代理来源定义
type Source struct {
	URL      string
	Protocol string // http 或 socks5
}

// 快速更新源（5-30分钟更新）- 用于紧急和补充模式
var fastUpdateSources = []Source{
	// jhao104/proxy_pool 中与现有列表不重复的动态来源。
	{"https://proxylist.geonode.com/api/proxy-list?filterLastChecked=10&page=1&limit=100&sort_by=lastChecked&sort_type=desc", "http"},
	{"https://proxy.scdn.io/get_proxies.php?protocol=http&country=&per_page=100&page=1", "http"},
	{"https://roundproxies.com/api/get-free-proxies/?limit=50&page=1&sort_by=lastChecked&sort_type=desc", "http"},
	// ProxyScraper - 每30分钟更新
	{"https://raw.githubusercontent.com/ProxyScraper/ProxyScraper/main/http.txt", "http"},
	{"https://raw.githubusercontent.com/ProxyScraper/ProxyScraper/main/socks5.txt", "socks5"},
	// monosans - 每小时更新
	{"https://raw.githubusercontent.com/monosans/proxy-list/main/proxies/http.txt", "http"},
	// prxchk - 频繁更新
	{"https://raw.githubusercontent.com/prxchk/proxy-list/main/http.txt", "http"},
	{"https://raw.githubusercontent.com/prxchk/proxy-list/main/socks5.txt", "socks5"},
	// sunny9577 - 自动抓取更新
	{"https://cdn.jsdelivr.net/gh/sunny9577/proxy-scraper/generated/http_proxies.txt", "http"},
	{"https://cdn.jsdelivr.net/gh/sunny9577/proxy-scraper/generated/socks5_proxies.txt", "socks5"},
}

// 慢速更新源（每天更新）- 用于优化轮换模式
var slowUpdateSources = []Source{
	// TheSpeedX - 每天更新，量大
	{"https://raw.githubusercontent.com/TheSpeedX/SOCKS-List/master/http.txt", "http"},
	{"https://raw.githubusercontent.com/TheSpeedX/SOCKS-List/master/socks5.txt", "socks5"},
	// monosans SOCKS
	{"https://raw.githubusercontent.com/monosans/proxy-list/main/proxies/socks4.txt", "socks5"},
	{"https://raw.githubusercontent.com/monosans/proxy-list/main/proxies/socks5.txt", "socks5"},
	// databay-labs - 备用源
	{"https://cdn.jsdelivr.net/gh/databay-labs/free-proxy-list/http.txt", "http"},
	{"https://cdn.jsdelivr.net/gh/databay-labs/free-proxy-list/socks5.txt", "socks5"},
	// Anonym0usWork1221 - 量大质量尚可
	{"https://cdn.jsdelivr.net/gh/Anonym0usWork1221/Free-Proxies/proxy_files/http_proxies.txt", "http"},
	{"https://cdn.jsdelivr.net/gh/Anonym0usWork1221/Free-Proxies/proxy_files/socks5_proxies.txt", "socks5"},
	// ALIILAPRO
	{"https://cdn.jsdelivr.net/gh/ALIILAPRO/Proxy/http.txt", "http"},
	// vakhov/fresh-proxy-list
	{"https://cdn.jsdelivr.net/gh/vakhov/fresh-proxy-list/http.txt", "http"},
	{"https://cdn.jsdelivr.net/gh/vakhov/fresh-proxy-list/socks5.txt", "socks5"},
	// Zaeem20
	{"https://cdn.jsdelivr.net/gh/Zaeem20/FREE_PROXIES_LIST/http.txt", "http"},
	// hookzof - socks5 专项
	{"https://cdn.jsdelivr.net/gh/hookzof/socks5_list/proxy.txt", "socks5"},
	// proxy4parsing
	{"https://cdn.jsdelivr.net/gh/proxy4parsing/proxy-list/http.txt", "http"},
	{"https://cdn.jsdelivr.net/gh/proxy4parsing/proxy-list/socks5.txt", "socks5"},
}

// 所有源
var allSources = append(fastUpdateSources, slowUpdateSources...)

type Fetcher struct {
	sources       []Source
	client        *http.Client
	sourceManager *SourceManager
}

func New(httpURL, socks5URL string, sourceManager *SourceManager) *Fetcher {
	return &Fetcher{
		sources:       allSources,
		sourceManager: sourceManager,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (f *Fetcher) RecordValidationBatch(url string, stats ValidationStats) {
	if f.sourceManager != nil {
		f.sourceManager.RecordValidationBatch(url, stats)
	}
}

func (f *Fetcher) GetSourceStats() ([]map[string]interface{}, error) {
	if f.sourceManager == nil {
		return []map[string]interface{}{}, nil
	}
	return f.sourceManager.GetSourceStats()
}

// FetchSmart 智能抓取：根据模式和协议需求选择源
func (f *Fetcher) FetchSmart(mode string, preferredProtocol string) ([]storage.Proxy, error) {
	var sources []Source

	switch mode {
	case "emergency":
		// 紧急模式仍遵守传输与质量断路器，避免持续重扫已知低质量源。
		sources = f.filterAvailableSources(allSources, preferredProtocol, false)
		log.Printf("[fetch] 🚨 紧急模式: 使用 %d 个可用源（遵守质量冷却）", len(sources))

	case "refill":
		// 补充模式：使用快更新源
		sources = f.filterAvailableSources(fastUpdateSources, preferredProtocol, false)
		log.Printf("[fetch] 🔄 补充模式: 使用 %d 个快更新源", len(sources))

	case "optimize":
		// 优化模式：随机选择2-3个慢更新源
		sources = f.selectRandomSources(slowUpdateSources, 3, preferredProtocol)
		log.Printf("[fetch] ⚡ 优化模式: 使用 %d 个源", len(sources))

	default:
		sources = f.filterAvailableSources(fastUpdateSources, preferredProtocol, false)
	}

	if len(sources) == 0 {
		return nil, fmt.Errorf("no available sources")
	}

	return f.fetchFromSources(sources)
}

// filterAvailableSources 过滤可用的源（通过断路器）
// ignoreCircuitBreaker: 是否忽略断路器（Emergency 模式下使用）
func (f *Fetcher) filterAvailableSources(sources []Source, preferredProtocol string, ignoreCircuitBreaker bool) []Source {
	var available []Source
	for _, src := range sources {
		// 检查断路器（紧急模式下忽略）
		if !ignoreCircuitBreaker && f.sourceManager != nil && !f.sourceManager.CanUseSource(src.URL) {
			continue
		}
		// 如果指定了协议偏好，优先该协议的源
		if preferredProtocol != "" && src.Protocol != "" && src.Protocol != preferredProtocol {
			continue
		}
		available = append(available, src)
	}
	return available
}

// selectRandomSources 随机选择N个源
func (f *Fetcher) selectRandomSources(sources []Source, count int, preferredProtocol string) []Source {
	available := f.filterAvailableSources(sources, preferredProtocol, false)
	if len(available) <= count {
		return available
	}

	// 随机打乱
	shuffled := make([]Source, len(available))
	copy(shuffled, available)
	for i := range shuffled {
		j := i + int(time.Now().UnixNano())%(len(shuffled)-i)
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}

	return shuffled[:count]
}

// fetchFromSources 从指定源列表抓取
func (f *Fetcher) fetchFromSources(sources []Source) ([]storage.Proxy, error) {
	type result struct {
		proxies []storage.Proxy
		source  Source
		err     error
	}

	ch := make(chan result, len(sources))
	for _, src := range sources {
		go func(s Source) {
			proxies, err := f.fetchFromURL(s.URL, s.Protocol)
			ch <- result{proxies: proxies, source: s, err: err}
		}(src)
	}

	var all []storage.Proxy
	seen := make(map[string]bool)
	for range sources {
		r := <-ch
		if r.err != nil {
			log.Printf("[fetch] ❌ %s error: %v", r.source.URL, r.err)
			if f.sourceManager != nil {
				f.sourceManager.RecordFail(r.source.URL, 3, 5, 30)
			}
			continue
		}

		// 记录成功
		if f.sourceManager != nil {
			f.sourceManager.RecordSuccess(r.source.URL)
		}

		// 去重
		var deduped []storage.Proxy
		for _, p := range r.proxies {
			if !seen[p.Address] {
				seen[p.Address] = true
				p.Origin = r.source.URL
				deduped = append(deduped, p)
			}
		}
		log.Printf("[fetch] ✅ %d 个 %s 代理 from %s", len(deduped), r.source.Protocol, r.source.URL)
		all = append(all, deduped...)
	}

	if len(all) == 0 {
		return nil, fmt.Errorf("no proxies fetched")
	}
	log.Printf("[fetch] 总共抓取: %d 个代理（去重后）", len(all))
	return all, nil
}

// Fetch 从所有来源并发抓取代理
func (f *Fetcher) Fetch() ([]storage.Proxy, error) {
	type result struct {
		proxies []storage.Proxy
		source  Source
		err     error
	}

	ch := make(chan result, len(f.sources))
	for _, src := range f.sources {
		go func(s Source) {
			proxies, err := f.fetchFromURL(s.URL, s.Protocol)
			ch <- result{proxies: proxies, source: s, err: err}
		}(src)
	}

	var all []storage.Proxy
	seen := make(map[string]bool)
	for range f.sources {
		r := <-ch
		if r.err != nil {
			log.Printf("fetch %s error: %v", r.source.URL, r.err)
			continue
		}
		// 去重
		var deduped []storage.Proxy
		for _, p := range r.proxies {
			if !seen[p.Address] {
				seen[p.Address] = true
				p.Origin = r.source.URL
				deduped = append(deduped, p)
			}
		}
		log.Printf("fetched %d %s proxies from %s", len(deduped), r.source.Protocol, r.source.URL)
		all = append(all, deduped...)
	}

	if len(all) == 0 {
		return nil, fmt.Errorf("no proxies fetched")
	}
	log.Printf("total fetched: %d proxies (deduped)", len(all))
	return all, nil
}

func (f *Fetcher) fetchFromURL(url, protocol string) ([]storage.Proxy, error) {
	resp, err := f.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return parseProxyPayload(data, protocol), nil
}

var embeddedAddressPattern = regexp.MustCompile(`(?m)(?:https?://|socks[45]?://)?((?:\d{1,3}\.){3}\d{1,3}):(\d{2,5})`)
var tableAddressPattern = regexp.MustCompile(`(?is)((?:\d{1,3}\.){3}\d{1,3})\s*</td>\s*<td[^>]*>\s*(\d{2,5})`)

func validProxyAddress(ip, port string) (string, bool) {
	parsed := net.ParseIP(ip)
	portNumber, err := strconv.Atoi(port)
	if parsed == nil || parsed.To4() == nil || err != nil || portNumber < 1 || portNumber > 65535 {
		return "", false
	}
	return net.JoinHostPort(ip, port), true
}

func appendPayloadProxy(result *[]storage.Proxy, seen map[string]bool, address, protocol string) {
	if address == "" || seen[address] {
		return
	}
	seen[address] = true
	if protocol != "http" && protocol != "socks5" {
		return
	}
	*result = append(*result, storage.Proxy{Address: address, Protocol: protocol})
}

func protocolFromJSON(item map[string]interface{}, fallback string) string {
	values := make([]string, 0, 2)
	if value, ok := item["protocol"].(string); ok {
		values = append(values, value)
	}
	if list, ok := item["protocols"].([]interface{}); ok {
		for _, value := range list {
			values = append(values, fmt.Sprint(value))
		}
	}
	for _, candidate := range values {
		switch strings.ToLower(strings.TrimSpace(candidate)) {
		case "http", "https":
			return "http"
		case "socks5":
			return "socks5"
		}
	}
	if len(values) > 0 {
		return ""
	}
	return fallback
}

func collectJSONProxies(value interface{}, protocol string, result *[]storage.Proxy, seen map[string]bool) {
	switch typed := value.(type) {
	case map[string]interface{}:
		ip := fmt.Sprint(typed["ip"])
		port := fmt.Sprint(typed["port"])
		if address, ok := validProxyAddress(ip, port); ok {
			appendPayloadProxy(result, seen, address, protocolFromJSON(typed, protocol))
		}
		for _, child := range typed {
			collectJSONProxies(child, protocol, result, seen)
		}
	case []interface{}:
		for _, child := range typed {
			collectJSONProxies(child, protocol, result, seen)
		}
	}
}

// parseProxyPayload supports the original one-address-per-line feeds plus the
// JSON/HTML response shapes used by Geonode, SCDN and RoundProxies.
func parseProxyPayload(data []byte, protocol string) []storage.Proxy {
	result := make([]storage.Proxy, 0)
	seen := make(map[string]bool)
	var decoded interface{}
	if json.Unmarshal(data, &decoded) == nil {
		collectJSONProxies(decoded, protocol, &result, seen)
	}
	for _, pattern := range []*regexp.Regexp{embeddedAddressPattern, tableAddressPattern} {
		for _, match := range pattern.FindAllSubmatch(data, -1) {
			if address, ok := validProxyAddress(string(match[1]), string(match[2])); ok {
				appendPayloadProxy(&result, seen, address, protocol)
			}
		}
	}
	return result
}
