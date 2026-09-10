package proxy

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"goproxy/storage"
)

const proxySessionDelimiter = "~"

// ProxySelector selects one upstream while excluding addresses that are already
// leased, cooling down, or tried by the current request.
type ProxySelector func(excludes []string) (*storage.Proxy, error)

type affinityLease struct {
	Proxy     storage.Proxy
	Session   string
	Target    string
	Scope     string
	ExpiresAt time.Time
	LastUsed  time.Time
}

type affinityCooldown struct {
	Until  time.Time
	Reason string
	Risk   bool
	Strike int
}

// ApplicationProxyStatus is intentionally aggregate-only. Upstream addresses
// never leave GoProxy's process through the status API.
type ApplicationProxyStatus struct {
	Enabled          bool    `json:"enabled"`
	LeaseTTLSeconds  int     `json:"lease_ttl_seconds"`
	ActiveLeases     int     `json:"active_leases"`
	CoolingProxies   int     `json:"cooling_proxies"`
	Requests         int64   `json:"requests"`
	Successes        int64   `json:"successes"`
	Failures         int64   `json:"failures"`
	RiskFailures     int64   `json:"risk_failures"`
	SuccessRate      float64 `json:"success_rate"`
	LastFailure      string  `json:"last_failure"`
	LastFailureAt    string  `json:"last_failure_at"`
	OldestCooldownAt string  `json:"oldest_cooldown_at"`
}

// AffinityManager provides bounded per-task stickiness and application-aware
// cooldowns. State is deliberately in-memory: a process restart clears leases
// and lets the normal pool validator remain the source of durable truth.
type AffinityManager struct {
	mu sync.Mutex

	leaseTTL        time.Duration
	failureCooldown time.Duration
	riskCooldown    time.Duration
	maxRiskCooldown time.Duration

	leases        map[string]affinityLease
	owners        map[string]string
	lastBySession map[string]string
	cooldowns     map[string]affinityCooldown
	riskStrikes   map[string]int

	requests      int64
	successes     int64
	failures      int64
	riskFailures  int64
	lastFailure   string
	lastFailureAt time.Time
}

func durationFromEnv(name string, fallback, minimum time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return fallback
	}
	value := time.Duration(seconds) * time.Second
	if value < minimum {
		return minimum
	}
	return value
}

func NewAffinityManager() *AffinityManager {
	return &AffinityManager{
		leaseTTL:        durationFromEnv("APP_LEASE_TTL_SECONDS", 3*time.Minute, 30*time.Second),
		failureCooldown: durationFromEnv("APP_FAILURE_COOLDOWN_SECONDS", 5*time.Minute, 30*time.Second),
		riskCooldown:    durationFromEnv("APP_RISK_COOLDOWN_SECONDS", 30*time.Minute, time.Minute),
		maxRiskCooldown: durationFromEnv("APP_MAX_RISK_COOLDOWN_SECONDS", 12*time.Hour, time.Minute),
		leases:          make(map[string]affinityLease),
		owners:          make(map[string]string),
		lastBySession:   make(map[string]string),
		cooldowns:       make(map[string]affinityCooldown),
		riskStrikes:     make(map[string]int),
	}
}

func normalizeAffinityValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	if len(value) > 160 {
		value = value[:160]
	}
	return value
}

func splitSessionUsername(username, configured string) (string, bool) {
	if username == configured {
		return "", true
	}
	prefix := configured + proxySessionDelimiter
	if configured == "" || !strings.HasPrefix(username, prefix) {
		return "", false
	}
	return normalizeAffinityValue(strings.TrimPrefix(username, prefix), ""), true
}

func applicationTarget(hostport string) string {
	host := strings.ToLower(strings.TrimSpace(hostport))
	if colon := strings.LastIndex(host, ":"); colon > -1 && !strings.Contains(host[colon+1:], "]") {
		host = strings.Trim(host[:colon], "[]")
	}
	if strings.HasSuffix(host, "bilibili.com") || strings.HasSuffix(host, "biliapi.net") {
		return "bilibili"
	}
	return normalizeAffinityValue(host, "general")
}

func leaseKey(session, scope string) string {
	return normalizeAffinityValue(session, "anonymous") + "\x00" + normalizeAffinityValue(scope, "default")
}

func appendUnique(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	result := make([]string, 0, len(values)+len(additions))
	for _, value := range append(values, additions...) {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (m *AffinityManager) cleanupLocked(now time.Time) {
	for key, lease := range m.leases {
		if now.Before(lease.ExpiresAt) {
			continue
		}
		delete(m.leases, key)
		if m.owners[lease.Proxy.Address] == key {
			delete(m.owners, lease.Proxy.Address)
		}
		if m.lastBySession[lease.Session] == key {
			delete(m.lastBySession, lease.Session)
		}
	}
	for address, cooldown := range m.cooldowns {
		if !now.Before(cooldown.Until) {
			delete(m.cooldowns, address)
		}
	}
}

// Select reuses a healthy lease for one task and scope. Different concurrent
// sessions cannot share the same upstream address.
func (m *AffinityManager) Select(session, target, scope string, tried []string, selector ProxySelector) (*storage.Proxy, error) {
	if selector == nil {
		return nil, fmt.Errorf("proxy selector is required")
	}
	session = normalizeAffinityValue(session, "")
	target = normalizeAffinityValue(target, "general")
	scope = normalizeAffinityValue(scope, "default")
	if session == "" {
		return selector(tried)
	}

	now := time.Now()
	key := leaseKey(session, scope)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked(now)

	if lease, ok := m.leases[key]; ok {
		cooldown, cooling := m.cooldowns[lease.Proxy.Address]
		isTried := false
		for _, address := range tried {
			if address == lease.Proxy.Address {
				isTried = true
				break
			}
		}
		if !cooling && !isTried && now.Before(lease.ExpiresAt) {
			lease.LastUsed = now
			m.leases[key] = lease
			m.lastBySession[session] = key
			proxyCopy := lease.Proxy
			return &proxyCopy, nil
		}
		_ = cooldown
		delete(m.leases, key)
		if m.owners[lease.Proxy.Address] == key {
			delete(m.owners, lease.Proxy.Address)
		}
	}

	excludes := appendUnique(nil, tried...)
	for address := range m.cooldowns {
		excludes = appendUnique(excludes, address)
	}
	for address, owner := range m.owners {
		if owner != key {
			excludes = appendUnique(excludes, address)
		}
	}
	selected, err := selector(excludes)
	if err != nil {
		return nil, err
	}
	lease := affinityLease{
		Proxy:     *selected,
		Session:   session,
		Target:    target,
		Scope:     scope,
		ExpiresAt: now.Add(m.leaseTTL),
		LastUsed:  now,
	}
	m.leases[key] = lease
	m.owners[selected.Address] = key
	m.lastBySession[session] = key
	proxyCopy := *selected
	return &proxyCopy, nil
}

func isRiskReason(reason string) bool {
	reason = strings.ToLower(reason)
	for _, marker := range []string{"412", "429", "-352", "-509", "risk", "风控", "precondition"} {
		if strings.Contains(reason, marker) {
			return true
		}
	}
	return false
}

func (m *AffinityManager) cooldownForRiskLocked(address string) (time.Duration, int) {
	strike := m.riskStrikes[address] + 1
	m.riskStrikes[address] = strike
	duration := m.riskCooldown
	for i := 1; i < strike && duration < m.maxRiskCooldown; i++ {
		duration *= 2
	}
	if duration > m.maxRiskCooldown {
		duration = m.maxRiskCooldown
	}
	return duration, strike
}

// Feedback applies the application result to the most recently used lease for
// this session. A failed lease is always invalidated so the next retry must use
// a different upstream.
func (m *AffinityManager) Feedback(session string, success bool, reason string) bool {
	session = normalizeAffinityValue(session, "")
	if session == "" {
		return false
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked(now)
	key, ok := m.lastBySession[session]
	if !ok {
		return false
	}
	lease, ok := m.leases[key]
	if !ok {
		delete(m.lastBySession, session)
		return false
	}
	m.requests++
	if success {
		m.successes++
		lease.ExpiresAt = now.Add(m.leaseTTL)
		lease.LastUsed = now
		m.leases[key] = lease
		if strikes := m.riskStrikes[lease.Proxy.Address]; strikes > 0 {
			m.riskStrikes[lease.Proxy.Address] = strikes - 1
		}
		return true
	}

	m.failures++
	risk := isRiskReason(reason)
	duration := m.failureCooldown
	strike := 0
	if risk {
		m.riskFailures++
		duration, strike = m.cooldownForRiskLocked(lease.Proxy.Address)
	}
	m.cooldowns[lease.Proxy.Address] = affinityCooldown{
		Until:  now.Add(duration),
		Reason: normalizeAffinityValue(reason, "request failed"),
		Risk:   risk,
		Strike: strike,
	}
	m.lastFailure = normalizeAffinityValue(reason, "request failed")
	m.lastFailureAt = now
	delete(m.leases, key)
	delete(m.lastBySession, session)
	if m.owners[lease.Proxy.Address] == key {
		delete(m.owners, lease.Proxy.Address)
	}
	return true
}

func (m *AffinityManager) Release(session string) int {
	session = normalizeAffinityValue(session, "")
	if session == "" {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	released := 0
	for key, lease := range m.leases {
		if lease.Session != session {
			continue
		}
		delete(m.leases, key)
		if m.owners[lease.Proxy.Address] == key {
			delete(m.owners, lease.Proxy.Address)
		}
		released++
	}
	delete(m.lastBySession, session)
	return released
}

func (m *AffinityManager) Status() ApplicationProxyStatus {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked(now)
	status := ApplicationProxyStatus{
		Enabled:         true,
		LeaseTTLSeconds: int(m.leaseTTL / time.Second),
		ActiveLeases:    len(m.leases),
		CoolingProxies:  len(m.cooldowns),
		Requests:        m.requests,
		Successes:       m.successes,
		Failures:        m.failures,
		RiskFailures:    m.riskFailures,
		LastFailure:     m.lastFailure,
	}
	if m.requests > 0 {
		status.SuccessRate = float64(m.successes) * 100 / float64(m.requests)
	}
	if !m.lastFailureAt.IsZero() {
		status.LastFailureAt = m.lastFailureAt.Format(time.RFC3339)
	}
	until := make([]time.Time, 0, len(m.cooldowns))
	for _, cooldown := range m.cooldowns {
		until = append(until, cooldown.Until)
	}
	if len(until) > 0 {
		sort.Slice(until, func(i, j int) bool { return until[i].Before(until[j]) })
		status.OldestCooldownAt = until[0].Format(time.RFC3339)
	}
	return status
}
