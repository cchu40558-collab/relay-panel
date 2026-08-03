package service

import (
	"fmt"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"gorm.io/gorm"
)

const (
	lineStatusScheduled    = "scheduled"
	lineStatusExpiring     = "expiring"
	lineStatusExpired      = "expired"
	lineStatusExpiryFailed = "expiry_failed"
)

type LineValidityReconcileResult struct {
	Started     int
	Expired     int
	RestartXray bool
}

func normalizeLineValidity(validFrom, validUntil *int64, now time.Time) (int64, int64, error) {
	from := int64(0)
	until := int64(0)
	if validFrom != nil {
		from = *validFrom
	}
	if validUntil != nil {
		until = *validUntil
	}
	if from < 0 || until < 0 {
		return 0, 0, fmt.Errorf("line validity time cannot be negative")
	}
	if until > 0 && until <= now.Unix() {
		return 0, 0, fmt.Errorf("line expiry time must be in the future")
	}
	if from > 0 && until > 0 && until <= from {
		return 0, 0, fmt.Errorf("line expiry time must be after the start time")
	}
	return from, until, nil
}

func lineInitialStatus(validFrom int64, now time.Time) string {
	if validFrom > now.Unix() {
		return lineStatusScheduled
	}
	return "pending_apply"
}

func validateLineCanApply(line model.LineProfile, now time.Time) error {
	if line.ManualReenableRequired || line.Status == lineStatusExpired || line.Status == lineStatusExpiring || line.Status == lineStatusExpiryFailed {
		return fmt.Errorf("line has expired; use renew and re-enable")
	}
	if line.ValidFrom > now.Unix() {
		return fmt.Errorf("line is scheduled to start at %s", time.Unix(line.ValidFrom, 0).UTC().Format(time.RFC3339))
	}
	if line.ValidUntil > 0 && line.ValidUntil <= now.Unix() {
		return fmt.Errorf("line validity period has ended")
	}
	return nil
}

func (s *LineService) UpdateLineValidity(id int, req LineValidityRequest) (*LineDetail, error) {
	if req.ValidUntil == nil {
		return nil, fmt.Errorf("line expiry time is required")
	}
	now := time.Now()
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		var line model.LineProfile
		if err := tx.First(&line, id).Error; err != nil {
			return err
		}
		if line.ManualReenableRequired {
			return fmt.Errorf("line has expired; use renew and re-enable")
		}
		from := line.ValidFrom
		validFrom, validUntil, err := normalizeLineValidity(&from, req.ValidUntil, now)
		if err != nil {
			return err
		}
		if err := tx.Model(&line).Updates(map[string]any{
			"valid_from":  validFrom,
			"valid_until": validUntil,
		}).Error; err != nil {
			return err
		}
		return createLineLog(tx, line.Id, "validity", "info", "有效期已延长，运行配置保持不变", fmt.Sprintf("validUntil=%s", time.Unix(validUntil, 0).UTC().Format(time.RFC3339)))
	})
	if err != nil {
		return nil, err
	}
	return s.getLine(id, false)
}

func (s *LineService) RenewLine(id int, req LineValidityRequest) (*LineDetail, error) {
	if req.ValidUntil == nil {
		return nil, fmt.Errorf("a new expiry time is required")
	}
	now := time.Now()
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		var line model.LineProfile
		if err := tx.First(&line, id).Error; err != nil {
			return err
		}
		if !line.ManualReenableRequired {
			return fmt.Errorf("line has not expired")
		}
		from := now.Unix()
		_, validUntil, err := normalizeLineValidity(&from, req.ValidUntil, now)
		if err != nil {
			return err
		}
		if err := tx.Model(&line).Updates(map[string]any{
			"valid_from":               from,
			"valid_until":              validUntil,
			"manual_reenable_required": false,
			"status":                   "pending_apply",
			"last_error":               "",
		}).Error; err != nil {
			return err
		}
		return createLineLog(tx, line.Id, "renew", "info", "已续期，等待人工重新启用", fmt.Sprintf("validUntil=%s", time.Unix(validUntil, 0).UTC().Format(time.RFC3339)))
	})
	if err != nil {
		return nil, err
	}
	return s.GetLine(id)
}

func (s *LineService) ReconcileLineValidity(now time.Time) (LineValidityReconcileResult, error) {
	result := LineValidityReconcileResult{}
	db := database.GetDB()

	var scheduled []model.LineProfile
	if err := db.Where("status = ? AND manual_reenable_required = ? AND valid_from > 0 AND valid_from <= ? AND (valid_until = 0 OR valid_until > ?)", lineStatusScheduled, false, now.Unix(), now.Unix()).Find(&scheduled).Error; err != nil {
		return result, err
	}
	for _, line := range scheduled {
		claim := db.Model(&model.LineProfile{}).Where("id = ? AND status = ? AND manual_reenable_required = ?", line.Id, lineStatusScheduled, false).Update("status", "pending_apply")
		if claim.Error != nil || claim.RowsAffected == 0 {
			continue
		}
		if _, err := s.ApplyLine(line.Id); err != nil {
			continue
		}
		result.Started++
		result.RestartXray = true
	}

	var expiring []model.LineProfile
	if err := db.Where("(valid_until > 0 AND valid_until <= ? AND status <> ?) OR status IN ?", now.Unix(), lineStatusExpired, []string{lineStatusExpiring, lineStatusExpiryFailed}).Find(&expiring).Error; err != nil {
		return result, err
	}
	for _, line := range expiring {
		restarted, err := s.expireLine(line.Id, now)
		if restarted {
			result.RestartXray = true
		}
		if err != nil {
			continue
		}
		result.Expired++
	}
	return result, nil
}

func (s *LineService) expireLine(id int, now time.Time) (bool, error) {
	db := database.GetDB()
	var line model.LineProfile
	if err := db.First(&line, id).Error; err != nil {
		return false, err
	}
	if line.Status == lineStatusExpired {
		return false, nil
	}

	if line.Status != lineStatusExpiring && line.Status != lineStatusExpiryFailed {
		claim := db.Model(&model.LineProfile{}).Where("id = ? AND status <> ?", id, lineStatusExpired).Updates(map[string]any{
			"status":                   lineStatusExpiring,
			"manual_reenable_required": true,
			"expired_at":               now.Unix(),
		})
		if claim.Error != nil || claim.RowsAffected == 0 {
			return false, claim.Error
		}
	}

	inboundTag := fmt.Sprintf("line-%d-in", line.Id)
	// Disable first so a subsequent immediate Xray restart cuts existing sessions
	// even if later cleanup of Nginx or template artifacts fails.
	if err := db.Model(&model.Inbound{}).Where("tag = ?", inboundTag).Update("enable", false).Error; err != nil {
		return false, s.recordLineExpiryFailure(line.Id, err)
	}

	config := decodeLineConfig(line.ConfigJSON)
	if _, err := removeLineNginxConfig(line, config); err != nil {
		return true, s.recordLineExpiryFailure(line.Id, err)
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		if err := removeLineTemplateArtifacts(tx, line.OutboundTag, inboundTag); err != nil {
			return err
		}
		if err := removeLineManagedClient(tx, line.Id); err != nil {
			return err
		}
		if err := tx.Where("tag = ?", inboundTag).Delete(&model.Inbound{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.LineProfile{}).Where("id = ?", line.Id).Updates(map[string]any{
			"status":                   lineStatusExpired,
			"manual_reenable_required": true,
			"expired_at":               now.Unix(),
			"inbound_id":               nil,
			"last_error":               "线路有效期已结束，已强制断开连接",
		}).Error; err != nil {
			return err
		}
		return createLineLog(tx, line.Id, "expiry", "warning", "线路已到期并强制断开", "已移除 Xray 入站、路由、受管用户和 Nginx 配置；需要人工续期并重新启用")
	})
	if err != nil {
		return true, s.recordLineExpiryFailure(line.Id, err)
	}
	return true, nil
}

func (s *LineService) recordLineExpiryFailure(id int, cause error) error {
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.LineProfile{}).Where("id = ?", id).Updates(map[string]any{
			"status":                   lineStatusExpiryFailed,
			"manual_reenable_required": true,
			"last_error":               fmt.Sprintf("线路到期清理异常：%v", cause),
		}).Error; err != nil {
			return err
		}
		return createLineLog(tx, id, "expiry", "error", "线路已先禁用，清理将在后台重试", cause.Error())
	})
}

func (s *LineService) LineCanServe(id int, now time.Time) error {
	var line model.LineProfile
	if err := database.GetDB().First(&line, id).Error; err != nil {
		return err
	}
	return validateLineCanApply(line, now)
}
