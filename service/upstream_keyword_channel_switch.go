package service

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const (
	upstreamKeywordCaptureSwitchKeyPrefix         = "new-api:upstream_keyword_capture:switch:"
	upstreamKeywordCaptureCurrentRetrySourceKey   = "upstream_keyword_capture_current_retry_source_channel_id"
	defaultUpstreamKeywordCaptureSwitchCount      = 1
	defaultUpstreamKeywordCaptureSwitchTTLSeconds = 600
)

type upstreamKeywordCaptureSwitchMarker struct {
	TargetChannelID int
	Remaining       int
	ExpiresAt       time.Time
}

var (
	upstreamKeywordCaptureSwitchMu     sync.Mutex
	upstreamKeywordCaptureSwitchMemory = map[int]upstreamKeywordCaptureSwitchMarker{}
	upstreamKeywordCaptureSwitchScript = redis.NewScript(`
local v = redis.call("GET", KEYS[1])
if not v then
  return ""
end
local target, count = string.match(v, "^(%d+):(%d+)$")
if not target then
  redis.call("DEL", KEYS[1])
  return ""
end
count = tonumber(count)
if not count or count <= 1 then
  redis.call("DEL", KEYS[1])
else
  local ttl = redis.call("TTL", KEYS[1])
  local next_value = target .. ":" .. tostring(count - 1)
  if ttl and ttl > 0 then
    redis.call("SETEX", KEYS[1], ttl, next_value)
  else
    redis.call("SET", KEYS[1], next_value)
  end
end
return target
`)
)

func upstreamKeywordCaptureSwitchKey(sourceChannelID int) string {
	return fmt.Sprintf("%s%d", upstreamKeywordCaptureSwitchKeyPrefix, sourceChannelID)
}

func normalizeUpstreamKeywordCaptureSwitchCount(count int) int {
	if count <= 0 {
		return defaultUpstreamKeywordCaptureSwitchCount
	}
	return count
}

func NormalizeUpstreamKeywordCaptureSwitchCountForLog(count int) int {
	return normalizeUpstreamKeywordCaptureSwitchCount(count)
}

func normalizeUpstreamKeywordCaptureSwitchTTLSeconds(ttlSeconds int) int {
	if ttlSeconds <= 0 {
		return defaultUpstreamKeywordCaptureSwitchTTLSeconds
	}
	return ttlSeconds
}

func NormalizeUpstreamKeywordCaptureSwitchTTLSecondsForLog(ttlSeconds int) int {
	return normalizeUpstreamKeywordCaptureSwitchTTLSeconds(ttlSeconds)
}

func ArmUpstreamKeywordCaptureChannelSwitch(sourceChannelID int, setting dto.ChannelSettings) bool {
	if sourceChannelID <= 0 ||
		!setting.UpstreamKeywordCaptureSwitchEnabled ||
		setting.UpstreamKeywordCaptureSwitchChannelId <= 0 ||
		setting.UpstreamKeywordCaptureSwitchChannelId == sourceChannelID {
		return false
	}

	targetChannelID := setting.UpstreamKeywordCaptureSwitchChannelId
	count := normalizeUpstreamKeywordCaptureSwitchCount(setting.UpstreamKeywordCaptureSwitchCount)
	ttl := time.Duration(normalizeUpstreamKeywordCaptureSwitchTTLSeconds(setting.UpstreamKeywordCaptureSwitchTTLSeconds)) * time.Second

	if common.RedisEnabled && common.RDB != nil {
		value := fmt.Sprintf("%d:%d", targetChannelID, count)
		if err := common.RedisSet(upstreamKeywordCaptureSwitchKey(sourceChannelID), value, ttl); err == nil {
			return true
		} else {
			common.SysError(fmt.Sprintf("failed to arm upstream keyword channel switch in redis: %v", err))
		}
	}

	upstreamKeywordCaptureSwitchMu.Lock()
	defer upstreamKeywordCaptureSwitchMu.Unlock()
	upstreamKeywordCaptureSwitchMemory[sourceChannelID] = upstreamKeywordCaptureSwitchMarker{
		TargetChannelID: targetChannelID,
		Remaining:       count,
		ExpiresAt:       time.Now().Add(ttl),
	}
	return true
}

func ConsumeUpstreamKeywordCaptureChannelSwitch(sourceChannelID int) (int, bool) {
	if sourceChannelID <= 0 {
		return 0, false
	}

	if common.RedisEnabled && common.RDB != nil {
		result, err := upstreamKeywordCaptureSwitchScript.Run(
			context.Background(),
			common.RDB,
			[]string{upstreamKeywordCaptureSwitchKey(sourceChannelID)},
		).Result()
		if err == nil {
			target, ok := parseUpstreamKeywordCaptureSwitchTarget(result)
			if ok {
				return target, true
			}
			return 0, false
		}
		if err != redis.Nil {
			common.SysError(fmt.Sprintf("failed to consume upstream keyword channel switch from redis: %v", err))
		}
	}

	upstreamKeywordCaptureSwitchMu.Lock()
	defer upstreamKeywordCaptureSwitchMu.Unlock()
	marker, ok := upstreamKeywordCaptureSwitchMemory[sourceChannelID]
	if !ok {
		return 0, false
	}
	if !marker.ExpiresAt.IsZero() && time.Now().After(marker.ExpiresAt) {
		delete(upstreamKeywordCaptureSwitchMemory, sourceChannelID)
		return 0, false
	}
	if marker.Remaining <= 1 {
		delete(upstreamKeywordCaptureSwitchMemory, sourceChannelID)
	} else {
		marker.Remaining--
		upstreamKeywordCaptureSwitchMemory[sourceChannelID] = marker
	}
	if marker.TargetChannelID <= 0 {
		return 0, false
	}
	return marker.TargetChannelID, true
}

func SetUpstreamKeywordCaptureCurrentRetrySource(c *gin.Context, sourceChannelID int) {
	if c == nil || sourceChannelID <= 0 {
		return
	}
	c.Set(upstreamKeywordCaptureCurrentRetrySourceKey, sourceChannelID)
}

func consumeUpstreamKeywordCaptureCurrentRetrySource(c *gin.Context) (int, bool) {
	if c == nil {
		return 0, false
	}
	sourceChannelID := c.GetInt(upstreamKeywordCaptureCurrentRetrySourceKey)
	c.Set(upstreamKeywordCaptureCurrentRetrySourceKey, 0)
	return sourceChannelID, sourceChannelID > 0
}

func GetUpstreamKeywordCaptureCurrentRetrySource(c *gin.Context) (int, bool) {
	if c == nil {
		return 0, false
	}
	sourceChannelID := c.GetInt(upstreamKeywordCaptureCurrentRetrySourceKey)
	return sourceChannelID, sourceChannelID > 0
}

func GetUpstreamKeywordCaptureCurrentRetrySourceForTest(c *gin.Context) (int, bool) {
	return GetUpstreamKeywordCaptureCurrentRetrySource(c)
}

func parseUpstreamKeywordCaptureSwitchTarget(value any) (int, bool) {
	switch v := value.(type) {
	case string:
		if v == "" {
			return 0, false
		}
		target, err := strconv.Atoi(v)
		return target, err == nil && target > 0
	case []byte:
		return parseUpstreamKeywordCaptureSwitchTarget(string(v))
	default:
		return 0, false
	}
}

func ApplyUpstreamKeywordCaptureChannelSwitch(c *gin.Context, selected *model.Channel, modelName string, group string) *model.Channel {
	if selected == nil {
		return selected
	}
	targetChannelID, ok := ConsumeUpstreamKeywordCaptureChannelSwitch(selected.Id)
	if !ok {
		return selected
	}
	target, err := model.CacheGetChannel(targetChannelID)
	if err != nil || target == nil {
		logger.LogWarn(c, fmt.Sprintf("upstream keyword channel switch target unavailable: source=%d target=%d err=%v", selected.Id, targetChannelID, err))
		return selected
	}
	if target.Status != common.ChannelStatusEnabled {
		logger.LogWarn(c, fmt.Sprintf("upstream keyword channel switch target disabled: source=%d target=%d", selected.Id, targetChannelID))
		return selected
	}
	if group != "" && group != "auto" && modelName != "" && !model.IsChannelEnabledForGroupModel(group, modelName, target.Id) {
		logger.LogWarn(c, fmt.Sprintf("upstream keyword channel switch target does not serve model/group: source=%d target=%d group=%s model=%s", selected.Id, target.Id, group, modelName))
		return selected
	}
	logger.LogInfo(c, fmt.Sprintf("upstream keyword channel switch applied: source=%d target=%d", selected.Id, target.Id))
	return target
}

func ApplyUpstreamKeywordCaptureCurrentRetryChannelSwitch(c *gin.Context, modelName string, group string) (*model.Channel, bool) {
	sourceChannelID, ok := consumeUpstreamKeywordCaptureCurrentRetrySource(c)
	if !ok {
		return nil, false
	}
	targetChannelID, ok := ConsumeUpstreamKeywordCaptureChannelSwitch(sourceChannelID)
	if !ok {
		return nil, false
	}
	target, err := model.CacheGetChannel(targetChannelID)
	if err != nil || target == nil {
		logger.LogWarn(c, fmt.Sprintf("upstream keyword current request replay switch target unavailable: source=%d target=%d err=%v", sourceChannelID, targetChannelID, err))
		return nil, false
	}
	if target.Status != common.ChannelStatusEnabled {
		logger.LogWarn(c, fmt.Sprintf("upstream keyword current request replay switch target disabled: source=%d target=%d", sourceChannelID, targetChannelID))
		return nil, false
	}
	if group != "" && group != "auto" && modelName != "" && !model.IsChannelEnabledForGroupModel(group, modelName, target.Id) {
		logger.LogWarn(c, fmt.Sprintf("upstream keyword current request replay switch target does not serve model/group: source=%d target=%d group=%s model=%s", sourceChannelID, target.Id, group, modelName))
		return nil, false
	}
	logger.LogInfo(c, fmt.Sprintf("upstream keyword current request replay channel switch applied: source=%d target=%d", sourceChannelID, target.Id))
	return target, true
}

func resetUpstreamKeywordCaptureChannelSwitchForTest() {
	upstreamKeywordCaptureSwitchMu.Lock()
	defer upstreamKeywordCaptureSwitchMu.Unlock()
	upstreamKeywordCaptureSwitchMemory = map[int]upstreamKeywordCaptureSwitchMarker{}
}

func ResetUpstreamKeywordCaptureChannelSwitchForTest() {
	resetUpstreamKeywordCaptureChannelSwitchForTest()
}
