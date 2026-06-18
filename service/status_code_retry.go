package service

import (
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

const (
	statusCodeRetryAttemptsKey      = "status_code_retry_attempts"
	statusCodeRetryPendingTargetKey = "status_code_retry_pending_target_channel_id"
	statusCodeRetryPendingSourceKey = "status_code_retry_pending_source_channel_id"
	statusCodeRetryPendingStatusKey = "status_code_retry_pending_status_code"
	defaultStatusCodeRetryCount     = 1
	defaultStatusCodeRetryInterval  = 0
)

func normalizeStatusCodeRetryCount(count int) int {
	if count <= 0 {
		return defaultStatusCodeRetryCount
	}
	return count
}

func NormalizeStatusCodeRetryCountForLog(count int) int {
	return normalizeStatusCodeRetryCount(count)
}

func normalizeStatusCodeRetryIntervalSeconds(intervalSeconds int) int {
	if intervalSeconds <= 0 {
		return defaultStatusCodeRetryInterval
	}
	return intervalSeconds
}

func NormalizeStatusCodeRetryIntervalSecondsForLog(intervalSeconds int) int {
	return normalizeStatusCodeRetryIntervalSeconds(intervalSeconds)
}

func ShouldStatusCodeRetry(setting dto.ChannelSettings, statusCode int) bool {
	if !setting.StatusCodeRetryEnabled || statusCode < 100 || statusCode > 599 {
		return false
	}
	ranges, err := operation_setting.ParseHTTPStatusCodeRanges(setting.StatusCodeRetryStatusCodes)
	if err != nil || len(ranges) == 0 {
		return false
	}
	for _, r := range ranges {
		if statusCode >= r.Start && statusCode <= r.End {
			return true
		}
	}
	return false
}

func statusCodeRetryAttemptKey(sourceChannelID int, statusCode int) string {
	return fmt.Sprintf("%d:%d", sourceChannelID, statusCode)
}

func ConsumeStatusCodeRetryAttempt(c *gin.Context, sourceChannelID int, statusCode int, setting dto.ChannelSettings) bool {
	if c == nil || sourceChannelID <= 0 || !ShouldStatusCodeRetry(setting, statusCode) {
		return false
	}
	count := normalizeStatusCodeRetryCount(setting.StatusCodeRetryCount)
	attempts, _ := c.Get(statusCodeRetryAttemptsKey)
	attemptMap, _ := attempts.(map[string]int)
	if attemptMap == nil {
		attemptMap = map[string]int{}
		c.Set(statusCodeRetryAttemptsKey, attemptMap)
	}
	key := statusCodeRetryAttemptKey(sourceChannelID, statusCode)
	if attemptMap[key] >= count {
		return false
	}
	attemptMap[key]++
	return true
}

func ResolveStatusCodeRetryTargetChannelID(sourceChannelID int, setting dto.ChannelSettings) int {
	if setting.StatusCodeRetryChannelId > 0 {
		return setting.StatusCodeRetryChannelId
	}
	return sourceChannelID
}

func HasPendingStatusCodeRetry(c *gin.Context) bool {
	if c == nil {
		return false
	}
	return c.GetInt(statusCodeRetryPendingTargetKey) > 0
}

func SetPendingStatusCodeRetryForTest(c *gin.Context, sourceChannelID int, targetChannelID int, statusCode int) {
	if c == nil {
		return
	}
	c.Set(statusCodeRetryPendingSourceKey, sourceChannelID)
	c.Set(statusCodeRetryPendingTargetKey, targetChannelID)
	c.Set(statusCodeRetryPendingStatusKey, statusCode)
}

func clearPendingStatusCodeRetry(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(statusCodeRetryPendingTargetKey, 0)
	c.Set(statusCodeRetryPendingSourceKey, 0)
	c.Set(statusCodeRetryPendingStatusKey, 0)
}

func PrepareStatusCodeRetry(c *gin.Context, source *model.Channel, err *types.NewAPIError, modelName string, group string) bool {
	if c == nil || source == nil || err == nil || types.IsSkipRetryError(err) {
		return false
	}
	setting := source.GetSetting()
	if !ConsumeStatusCodeRetryAttempt(c, source.Id, err.StatusCode, setting) {
		return false
	}
	targetChannelID := ResolveStatusCodeRetryTargetChannelID(source.Id, setting)
	target, ok := loadValidStatusCodeRetryTarget(c, source.Id, targetChannelID, modelName, group)
	if !ok {
		return false
	}
	interval := normalizeStatusCodeRetryIntervalSeconds(setting.StatusCodeRetryIntervalSeconds)
	if interval > 0 {
		logger.LogInfo(c, fmt.Sprintf("status code retry waiting: source=%d target=%d status=%d interval_seconds=%d", source.Id, target.Id, err.StatusCode, interval))
		time.Sleep(time.Duration(interval) * time.Second)
	}
	c.Set(statusCodeRetryPendingSourceKey, source.Id)
	c.Set(statusCodeRetryPendingTargetKey, target.Id)
	c.Set(statusCodeRetryPendingStatusKey, err.StatusCode)
	logger.LogInfo(c, fmt.Sprintf("status code retry armed: source=%d target=%d status=%d count=%d interval_seconds=%d", source.Id, target.Id, err.StatusCode, normalizeStatusCodeRetryCount(setting.StatusCodeRetryCount), interval))
	return true
}

func ApplyStatusCodeRetryCurrentChannel(c *gin.Context, modelName string, group string) (*model.Channel, bool) {
	if c == nil {
		return nil, false
	}
	targetChannelID := c.GetInt(statusCodeRetryPendingTargetKey)
	sourceChannelID := c.GetInt(statusCodeRetryPendingSourceKey)
	statusCode := c.GetInt(statusCodeRetryPendingStatusKey)
	clearPendingStatusCodeRetry(c)
	if targetChannelID <= 0 {
		return nil, false
	}
	target, ok := loadValidStatusCodeRetryTarget(c, sourceChannelID, targetChannelID, modelName, group)
	if !ok {
		return nil, false
	}
	logger.LogInfo(c, fmt.Sprintf("status code retry channel applied: source=%d target=%d status=%d", sourceChannelID, target.Id, statusCode))
	return target, true
}

func loadValidStatusCodeRetryTarget(c *gin.Context, sourceChannelID int, targetChannelID int, modelName string, group string) (*model.Channel, bool) {
	if targetChannelID <= 0 {
		return nil, false
	}
	target, err := model.CacheGetChannel(targetChannelID)
	if err != nil || target == nil {
		logger.LogWarn(c, fmt.Sprintf("status code retry target unavailable: source=%d target=%d err=%v", sourceChannelID, targetChannelID, err))
		return nil, false
	}
	if target.Status != common.ChannelStatusEnabled {
		logger.LogWarn(c, fmt.Sprintf("status code retry target disabled: source=%d target=%d", sourceChannelID, targetChannelID))
		return nil, false
	}
	if group != "" && group != "auto" && modelName != "" && !model.IsChannelEnabledForGroupModel(group, modelName, target.Id) {
		logger.LogWarn(c, fmt.Sprintf("status code retry target does not serve model/group: source=%d target=%d group=%s model=%s", sourceChannelID, target.Id, group, modelName))
		return nil, false
	}
	return target, true
}
