package validator

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/net/proxy"
	"goproxy/config"
	"goproxy/storage"
)

type Validator struct {
	concurrency   int
	timeout       time.Duration
	validateURL   string
	maxResponseMs int
	cfg           *config.Config
}

func concurrencyBuffer(total, concurrency int) int {
	if total < concurrency*10 {
		return total
	}
	return concurrency * 10
}

func New(concurrency, timeoutSec int, validateURL string) *Validator {
	cfg := config.Get()
	maxMs := 0
	if cfg != nil {
		maxMs = cfg.MaxResponseMs
	}
	return &Validator{
		concurrency:   concurrency,
		timeout:       time.Duration(timeoutSec) * time.Second,
		validateURL:   validateURL,
		maxResponseMs: maxMs,
		cfg:           cfg,
	}
}

type Result struct {
	Proxy        storage.Proxy
	Valid        bool
	Latency      time.Duration
	ExitIP       string
	ExitLocation string
	BaseSuccess  bool
	TLSSuccess   bool
	ExitSuccess  bool
	FailureStage string
}

const browserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/128.0.0.0 Safari/537.36"

// getExitIPInfo 通过代理获取出口 IP 和地理位置。ip-api 偶发限流或被代理
// 拦截时回退到 ipwho.is，避免把可用节点误判为无出口。
func getExitIPInfo(client *http.Client) (string, string) {
	resp, err := client.Get("http://ip-api.com/json/?fields=status,countryCode,city,query")
	if err == nil {
		var result struct {
			Status      string `json:"status"`
			Query       string `json:"query"`
			CountryCode string `json:"countryCode"`
			City        string `json:"city"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if decodeErr == nil && result.Status == "success" && result.Query != "" && result.CountryCode != "" {
			return result.Query, formatLocation(result.CountryCode, result.City)
		}
	}

	resp, err = client.Get("https://ipwho.is/?fields=success,ip,country_code,city")
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	var fallback struct {
		Success     bool   `json:"success"`
		IP          string `json:"ip"`
		CountryCode string `json:"country_code"`
		City        string `json:"city"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&fallback); err != nil || !fallback.Success || fallback.IP == "" || fallback.CountryCode == "" {
		return "", ""
	}
	return fallback.IP, formatLocation(fallback.CountryCode, fallback.City)
}

func formatLocation(countryCode, city string) string {
	if city == "" {
		return countryCode
	}
	return fmt.Sprintf("%s %s", countryCode, city)
}

// checkBilibiliHTTPS 使用最终业务目标验证真实 TLS 与 B 站可达性。
func checkBilibiliHTTPS(client *http.Client) (time.Duration, bool) {
	req, err := http.NewRequest(http.MethodGet, "https://api.bilibili.com/x/web-interface/nav", nil)
	if err != nil {
		return 0, false
	}
	req.Header.Set("User-Agent", browserUserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return latency, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return latency, false
	}
	var payload struct {
		Code int `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return latency, false
	}
	// 未登录的 -101 是正常可达；-412 明确表示命中 B 站风控。
	return latency, payload.Code != -412
}

// ValidateAll 并发验证所有代理，返回验证结果
func (v *Validator) ValidateAll(proxies []storage.Proxy) []Result {
	var results []Result
	for r := range v.ValidateStream(proxies) {
		results = append(results, r)
	}
	return results
}

// ValidateStream 并发验证，边验证边通过 channel 返回结果
func (v *Validator) ValidateStream(proxies []storage.Proxy) <-chan Result {
	ch := make(chan Result, concurrencyBuffer(len(proxies), v.concurrency))
	sem := make(chan struct{}, v.concurrency)
	var wg sync.WaitGroup

	go func() {
		for _, p := range proxies {
			wg.Add(1)
			sem <- struct{}{}
			go func(px storage.Proxy) {
				defer wg.Done()
				defer func() { <-sem }()
				ch <- v.validateOne(px)
			}(p)
		}
		wg.Wait()
		close(ch)
	}()

	return ch
}

// ValidateOne 验证单个代理是否可用，返回是否有效、延迟、出口IP和地理位置
func (v *Validator) ValidateOne(p storage.Proxy) (bool, time.Duration, string, string) {
	result := v.validateOne(p)
	return result.Valid, result.Latency, result.ExitIP, result.ExitLocation
}

func (v *Validator) validateOne(p storage.Proxy) Result {
	result := Result{Proxy: p}
	var client *http.Client
	var err error

	switch p.Protocol {
	case "http":
		client, err = newHTTPClient(p.Address, v.timeout)
	case "socks5":
		client, err = newSOCKS5Client(p.Address, v.timeout)
	default:
		log.Printf("unknown protocol %s for %s", p.Protocol, p.Address)
		result.FailureStage = "protocol"
		return result
	}

	if err != nil {
		result.FailureStage = "client"
		return result
	}

	start := time.Now()
	resp, err := client.Get(v.validateURL)
	baseLatency := time.Since(start)
	if err != nil {
		result.FailureStage = "connect"
		return result
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// 验证状态码（200 或 204 都接受）
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		result.Latency = baseLatency
		result.FailureStage = "base_status"
		return result
	}
	result.BaseSuccess = true

	// 最终使用场景是 B 站采集，因此直接用 B 站 HTTPS API 验证 TLS、
	// CONNECT/SOCKS 隧道和风控可达性，并以这段耗时作为节点延迟。
	bilibiliLatency, ok := checkBilibiliHTTPS(client)
	result.Latency = bilibiliLatency
	if !ok {
		result.FailureStage = "bilibili_tls"
		return result
	}
	result.TLSSuccess = true

	if v.maxResponseMs > 0 && bilibiliLatency > time.Duration(v.maxResponseMs)*time.Millisecond {
		result.FailureStage = "bilibili_slow"
		return result
	}

	// 获取出口 IP 和地理位置（仅在验证通过时）
	exitIP, exitLocation := getExitIPInfo(client)
	result.ExitIP = exitIP
	result.ExitLocation = exitLocation

	// 必须能获取到出口信息
	if exitIP == "" || exitLocation == "" {
		result.FailureStage = "exit_lookup"
		return result
	}
	result.ExitSuccess = true

	// 地理过滤：白名单优先，否则走黑名单
	if v.cfg != nil && len(exitLocation) >= 2 {
		countryCode := exitLocation[:2]
		if len(v.cfg.AllowedCountries) > 0 {
			// 白名单模式：不在白名单中则拒绝
			allowed := false
			for _, a := range v.cfg.AllowedCountries {
				if countryCode == a {
					allowed = true
					break
				}
			}
			if !allowed {
				result.FailureStage = "geo"
				return result
			}
		} else if len(v.cfg.BlockedCountries) > 0 {
			// 黑名单模式
			for _, blocked := range v.cfg.BlockedCountries {
				if countryCode == blocked {
					result.FailureStage = "geo"
					return result
				}
			}
		}
	}

	result.Valid = true
	return result
}

func newHTTPClient(address string, timeout time.Duration) (*http.Client, error) {
	proxyURL, err := url.Parse(fmt.Sprintf("http://%s", address))
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
		Timeout: timeout,
	}, nil
}

func newSOCKS5Client(address string, timeout time.Duration) (*http.Client, error) {
	dialer, err := proxy.SOCKS5("tcp", address, nil, proxy.Direct)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Transport: &http.Transport{
			Dial: dialer.Dial,
		},
		Timeout: timeout,
	}, nil
}
