package fetcher

import (
	"database/sql"
	"log"
	"sync"
	"time"
)

// ValidationStats 是一次抓取中某个来源的分阶段验证漏斗。
type ValidationStats struct {
	Candidates  int
	BaseSuccess int
	TLSSuccess  int
	ExitSuccess int
	Admitted    int
}

// SourceManager 代理源管理器（断路器）
type SourceManager struct {
	db *sql.DB
	mu sync.RWMutex
}

func NewSourceManager(db *sql.DB) *SourceManager {
	return &SourceManager{db: db}
}

// CanUseSource 判断源是否可用
func (sm *SourceManager) CanUseSource(url string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var status, qualityStatus string
	var disabledUntil, qualityDisabledUntil sql.NullTime
	err := sm.db.QueryRow(
		`SELECT status, disabled_until, quality_status, quality_disabled_until FROM source_status WHERE url = ?`,
		url,
	).Scan(&status, &disabledUntil, &qualityStatus, &qualityDisabledUntil)

	// 源不存在，默认可用
	if err != nil {
		return true
	}

	// 检查是否被禁用且还在冷却期
	if status == "disabled" && disabledUntil.Valid {
		if time.Now().Before(disabledUntil.Time) {
			return false
		}
		// 冷却期结束，重置状态
		sm.db.Exec(`UPDATE source_status SET status = 'active', consecutive_fails = 0 WHERE url = ?`, url)
		return true
	}

	// 抓取接口可访问，不代表其中的代理质量合格。低质量来源单独冷却，
	// emergency 模式同样遵守，避免反复验证同一批失效地址。
	if (qualityStatus == "degraded" || qualityStatus == "disabled") && qualityDisabledUntil.Valid {
		if time.Now().Before(qualityDisabledUntil.Time) {
			return false
		}
		sm.db.Exec(`UPDATE source_status SET quality_status = 'active', quality_disabled_until = NULL WHERE url = ?`, url)
	}

	return status != "disabled"
}

// RecordSuccess 记录源抓取成功
func (sm *SourceManager) RecordSuccess(url string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.db.Exec(`
		INSERT INTO source_status (url, success_count, consecutive_fails, last_success, status) 
		VALUES (?, 1, 0, CURRENT_TIMESTAMP, 'active')
		ON CONFLICT(url) DO UPDATE SET 
			success_count = success_count + 1,
			consecutive_fails = 0,
			last_success = CURRENT_TIMESTAMP,
			status = 'active'
	`, url)
}

// RecordFail 记录源抓取失败
func (sm *SourceManager) RecordFail(url string, failThreshold, disableThreshold, cooldownMinutes int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// 增加失败计数
	sm.db.Exec(`
		INSERT INTO source_status (url, fail_count, consecutive_fails, last_fail) 
		VALUES (?, 1, 1, CURRENT_TIMESTAMP)
		ON CONFLICT(url) DO UPDATE SET 
			fail_count = fail_count + 1,
			consecutive_fails = consecutive_fails + 1,
			last_fail = CURRENT_TIMESTAMP
	`, url)

	// 检查是否需要降级或禁用
	var consecutiveFails int
	sm.db.QueryRow(`SELECT consecutive_fails FROM source_status WHERE url = ?`, url).Scan(&consecutiveFails)

	if consecutiveFails >= disableThreshold {
		// 禁用源
		disabledUntil := time.Now().Add(time.Duration(cooldownMinutes) * time.Minute)
		sm.db.Exec(
			`UPDATE source_status SET status = 'disabled', disabled_until = ? WHERE url = ?`,
			disabledUntil, url,
		)
		log.Printf("[source] ⛔ 禁用源（连续失败%d次）: %s (冷却%d分钟)", consecutiveFails, url, cooldownMinutes)
	} else if consecutiveFails >= failThreshold {
		// 降级源
		sm.db.Exec(`UPDATE source_status SET status = 'degraded' WHERE url = ?`, url)
		log.Printf("[source] ⚠️  降级源（连续失败%d次）: %s", consecutiveFails, url)
	}
}

// RecordValidationBatch 聚合写入一次来源验证结果，并按真实 TLS 通过率执行质量冷却。
// 少于 50 个候选的来源不自动降级，避免小样本误判。
func (sm *SourceManager) RecordValidationBatch(url string, stats ValidationStats) {
	if url == "" || stats.Candidates <= 0 {
		return
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	_, _ = sm.db.Exec(`
		INSERT INTO source_status (
			url, candidate_total, base_success_total, tls_success_total, exit_success_total, admitted_total,
			last_candidates, last_base_success, last_tls_success, last_exit_success, last_admitted,
			last_validated, quality_status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, 'active')
		ON CONFLICT(url) DO UPDATE SET
			candidate_total = candidate_total + excluded.candidate_total,
			base_success_total = base_success_total + excluded.base_success_total,
			tls_success_total = tls_success_total + excluded.tls_success_total,
			exit_success_total = exit_success_total + excluded.exit_success_total,
			admitted_total = admitted_total + excluded.admitted_total,
			last_candidates = excluded.last_candidates,
			last_base_success = excluded.last_base_success,
			last_tls_success = excluded.last_tls_success,
			last_exit_success = excluded.last_exit_success,
			last_admitted = excluded.last_admitted,
			last_validated = CURRENT_TIMESTAMP
	`, url,
		stats.Candidates, stats.BaseSuccess, stats.TLSSuccess, stats.ExitSuccess, stats.Admitted,
		stats.Candidates, stats.BaseSuccess, stats.TLSSuccess, stats.ExitSuccess, stats.Admitted,
	)

	if stats.Candidates < 50 {
		return
	}

	tlsRate := float64(stats.TLSSuccess) / float64(stats.Candidates)
	if tlsRate >= 0.005 {
		_, _ = sm.db.Exec(`
			UPDATE source_status
			SET low_quality_streak = 0, quality_status = 'active', quality_disabled_until = NULL
			WHERE url = ?
		`, url)
		return
	}

	var streak int
	_ = sm.db.QueryRow(`SELECT low_quality_streak FROM source_status WHERE url = ?`, url).Scan(&streak)
	streak++
	qualityStatus := "degraded"
	cooldown := 30 * time.Minute
	if streak >= 3 {
		qualityStatus = "disabled"
		cooldown = 2 * time.Hour
	}
	until := time.Now().Add(cooldown)
	_, _ = sm.db.Exec(`
		UPDATE source_status
		SET low_quality_streak = ?, quality_status = ?, quality_disabled_until = ?
		WHERE url = ?
	`, streak, qualityStatus, until, url)
	log.Printf("[source] quality %s: tls=%d/%d (%.2f%%), cooldown=%s, %s",
		qualityStatus, stats.TLSSuccess, stats.Candidates, tlsRate*100, cooldown, url)
}

// GetSourceStats 获取所有源的统计信息
func (sm *SourceManager) GetSourceStats() ([]map[string]interface{}, error) {
	rows, err := sm.db.Query(`
		SELECT url, success_count, fail_count, consecutive_fails,
		       last_success, last_fail, status,
		       candidate_total, base_success_total, tls_success_total, exit_success_total, admitted_total,
		       last_candidates, last_base_success, last_tls_success, last_exit_success, last_admitted,
		       low_quality_streak, quality_status, quality_disabled_until, last_validated
		FROM source_status
		ORDER BY CASE WHEN last_candidates > 0 THEN 0 ELSE 1 END,
		         (last_tls_success * 1.0 / CASE WHEN last_candidates > 0 THEN last_candidates ELSE 1 END) DESC,
		         last_candidates DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []map[string]interface{}
	for rows.Next() {
		var url, status, qualityStatus string
		var successCount, failCount, consecutiveFails int
		var candidateTotal, baseTotal, tlsTotal, exitTotal, admittedTotal int
		var lastCandidates, lastBase, lastTLS, lastExit, lastAdmitted, lowQualityStreak int
		var lastSuccess, lastFail, qualityDisabledUntil, lastValidated sql.NullTime

		if err := rows.Scan(
			&url, &successCount, &failCount, &consecutiveFails, &lastSuccess, &lastFail, &status,
			&candidateTotal, &baseTotal, &tlsTotal, &exitTotal, &admittedTotal,
			&lastCandidates, &lastBase, &lastTLS, &lastExit, &lastAdmitted,
			&lowQualityStreak, &qualityStatus, &qualityDisabledUntil, &lastValidated,
		); err != nil {
			return nil, err
		}
		lastRate := 0.0
		if lastCandidates > 0 {
			lastRate = float64(lastAdmitted) / float64(lastCandidates)
		}

		stats = append(stats, map[string]interface{}{
			"url":                url,
			"success_count":      successCount,
			"fail_count":         failCount,
			"consecutive_fails":  consecutiveFails,
			"last_success":       lastSuccess,
			"last_fail":          lastFail,
			"status":             status,
			"candidate_total":    candidateTotal,
			"base_total":         baseTotal,
			"tls_total":          tlsTotal,
			"exit_total":         exitTotal,
			"admitted_total":     admittedTotal,
			"last_candidates":    lastCandidates,
			"last_base":          lastBase,
			"last_tls":           lastTLS,
			"last_exit":          lastExit,
			"last_admitted":      lastAdmitted,
			"last_admit_rate":    lastRate,
			"quality_status":     qualityStatus,
			"low_quality_streak": lowQualityStreak,
			"quality_until":      nullableTime(qualityDisabledUntil),
			"last_validated":     nullableTime(lastValidated),
		})
	}
	return stats, nil
}

func nullableTime(value sql.NullTime) interface{} {
	if !value.Valid {
		return nil
	}
	return value.Time
}
