package checker

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"goproxy/config"
	"goproxy/pool"
	"goproxy/storage"
	"goproxy/validator"
)

func boundedEnvInt(name string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value < minimum {
		return fallback
	}
	if maximum > 0 && value > maximum {
		return maximum
	}
	return value
}

// HealthChecker 健康检查器
type HealthChecker struct {
	storage   *storage.Storage
	validator *validator.Validator
	cfg       *config.Config
	poolMgr   *pool.Manager
}

func NewHealthChecker(s *storage.Storage, v *validator.Validator, cfg *config.Config, pm *pool.Manager) *HealthChecker {
	return &HealthChecker{
		storage:   s,
		validator: v,
		cfg:       cfg,
		poolMgr:   pm,
	}
}

// RunOnce 执行一次健康检查
func (hc *HealthChecker) RunOnce() {
	start := time.Now()
	log.Println("[health] 开始健康检查...")
	disableAfter := boundedEnvInt("APP_DISABLE_AFTER_FAILURES", 3, 2, 20)
	retentionHours := boundedEnvInt("APP_DELETE_AFTER_HOURS", 36, 24, 48)
	defer func() {
		deleted, err := hc.storage.DeleteExpiredFailed(retentionHours)
		if err != nil {
			log.Printf("[health] 清理过期失败代理失败: %v", err)
		} else if deleted > 0 {
			log.Printf("[health] 清理已隔离超过 %d 小时的免费代理: %d", retentionHours, deleted)
		}
	}()

	// 获取池子状态
	status, err := hc.poolMgr.GetStatus()
	if err != nil {
		log.Printf("[health] 获取状态失败: %v", err)
		return
	}

	// 健康状态且S级占比高时，跳过S级代理检查
	skipSGrade := status.State == "healthy"
	dist, _ := hc.storage.GetQualityDistribution()
	sGradeCount := dist["S"]
	totalCount := status.Total
	if totalCount > 0 && float64(sGradeCount)/float64(totalCount) > 0.3 {
		skipSGrade = true
	}

	// 批量获取需要检查的代理
	proxies, err := hc.storage.GetBatchForHealthCheck(hc.cfg.HealthCheckBatchSize, skipSGrade)
	if err != nil {
		log.Printf("[health] 获取检查批次失败: %v", err)
		return
	}

	if len(proxies) == 0 {
		log.Println("[health] 无需检查的代理")
		return
	}

	log.Printf("[health] 检查 %d 个代理（跳过S级=%v）", len(proxies), skipSGrade)

	// 执行验证
	validCount := 0
	removeCount := 0
	updateCount := 0

	for result := range hc.validator.ValidateStream(proxies) {
		if result.Valid {
			validCount++
			// 更新延迟和质量等级
			latencyMs := int(result.Latency.Milliseconds())
			if err := hc.storage.UpdateExitInfo(result.Proxy.Address, result.ExitIP, result.ExitLocation, latencyMs); err == nil {
				hc.storage.ResetFail(result.Proxy.Address)
				updateCount++
			}
		} else {
			// 首次失败降级，连续失败才禁用；不在健康检查中立即删除。
			hc.storage.MarkProxyFailure(result.Proxy.Address, false, disableAfter)
			if result.Proxy.FailCount+1 >= disableAfter {
				removeCount++
			}
		}
	}

	elapsed := time.Since(start)
	log.Printf("[health] 完成: 验证%d 有效%d 更新%d 禁用%d 耗时%v",
		len(proxies), validCount, updateCount, removeCount, elapsed)
}

// StartBackground 后台定时健康检查
func (hc *HealthChecker) StartBackground() {
	ticker := time.NewTicker(time.Duration(hc.cfg.HealthCheckInterval) * time.Minute)
	go func() {
		for range ticker.C {
			hc.RunOnce()
		}
	}()
	log.Printf("[health] 健康检查器已启动，间隔 %d 分钟", hc.cfg.HealthCheckInterval)
}
