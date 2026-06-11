package helper

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

const upstreamKeywordCaptureMaxBytes = 64 << 10

var requestBlockedTextStartRegexp = regexp.MustCompile(`(?i)"(?:text|delta)"\s*:\s*"request_blocked`)

var errUpstreamKeywordCapturedForRetry = errors.New("upstream keyword captured; retry current request with switched channel")

type UpstreamKeywordCaptureMatch struct {
	Matched  bool
	Keywords []string
}

func NormalizeUpstreamKeywordCaptureKeywords(keywords []string) []string {
	normalized := make([]string, 0, len(keywords))
	seen := make(map[string]struct{}, len(keywords))
	for _, keyword := range keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			continue
		}
		key := strings.ToLower(keyword)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, keyword)
	}
	return normalized
}

func MatchUpstreamKeywordCapturePayload(setting dto.ChannelSettings, payload string) UpstreamKeywordCaptureMatch {
	if !setting.UpstreamKeywordCaptureEnabled {
		return UpstreamKeywordCaptureMatch{}
	}

	keywords := NormalizeUpstreamKeywordCaptureKeywords(setting.UpstreamKeywordCaptureKeywords)
	if len(keywords) == 0 {
		return UpstreamKeywordCaptureMatch{}
	}
	if isUpstreamKeywordCaptureIgnoredPayload(payload) {
		return UpstreamKeywordCaptureMatch{}
	}

	lowerPayload := strings.ToLower(payload)
	matchedKeywords := make([]string, 0, len(keywords))
	for _, keyword := range keywords {
		if !strings.Contains(lowerPayload, strings.ToLower(keyword)) {
			continue
		}
		if isRequestBlockedKeyword(keyword) && !isRequestBlockedTextStartPayload(payload) {
			continue
		}
		matchedKeywords = append(matchedKeywords, keyword)
	}
	if len(matchedKeywords) == 0 {
		return UpstreamKeywordCaptureMatch{}
	}
	return UpstreamKeywordCaptureMatch{Matched: true, Keywords: matchedKeywords}
}

func isRequestBlockedKeyword(keyword string) bool {
	return strings.EqualFold(strings.TrimSpace(keyword), "REQUEST_BLOCKED")
}

func isRequestBlockedTextStartPayload(payload string) bool {
	data := strings.TrimSpace(payload)
	if strings.HasPrefix(data, "data:") {
		data = strings.TrimSpace(strings.TrimPrefix(data, "data:"))
	}
	if strings.HasPrefix(strings.ToLower(data), "request_blocked") {
		return true
	}
	return requestBlockedTextStartRegexp.MatchString(data)
}

func isUpstreamKeywordCaptureIgnoredPayload(payload string) bool {
	data := strings.TrimSpace(payload)
	if strings.HasPrefix(data, "data:") {
		data = strings.TrimSpace(strings.TrimPrefix(data, "data:"))
	}
	lowerData := strings.ToLower(data)

	// The Responses API stream can echo the entire request instructions/tools in
	// response metadata events. Those are not upstream refusals; they are request
	// echoes and can contain the literal REQUEST_BLOCKED policy text.
	if strings.Contains(lowerData, `"instructions":`) &&
		(strings.Contains(lowerData, `"type":"response.created"`) ||
			strings.Contains(lowerData, `"type":"response.in_progress"`) ||
			strings.Contains(lowerData, `"type":"response.completed"`)) {
		return true
	}

	// Tool-call argument events may contain our own shell/grep text, not the
	// upstream model's user-visible response.
	if strings.Contains(lowerData, `"type":"response.function_call_arguments`) ||
		(strings.Contains(lowerData, `"type":"response.output_item.done"`) &&
			strings.Contains(lowerData, `"type":"function_call"`)) {
		return true
	}

	return false
}

func CaptureUpstreamKeywordPayload(c *gin.Context, info *relaycommon.RelayInfo, source string, statusCode int, payload string) bool {
	setting := dto.ChannelSettings{}
	if info != nil && info.ChannelMeta != nil {
		setting = info.ChannelSetting
	}
	match := MatchUpstreamKeywordCapturePayload(setting, payload)
	if !match.Matched {
		return false
	}

	captureDir := "./logs"
	if common.LogDir != nil && *common.LogDir != "" {
		captureDir = *common.LogDir
	}
	_ = os.MkdirAll(captureDir, 0o755)

	now := time.Now()
	sum := sha256.Sum256([]byte(payload))
	fileName := fmt.Sprintf("upstream-keyword-hit-%s-%s.log", now.Format("20060102-150405.000000000"), hex.EncodeToString(sum[:6]))
	path := filepath.Join(captureDir, fileName)

	if len(payload) > upstreamKeywordCaptureMaxBytes {
		payload = payload[:upstreamKeywordCaptureMaxBytes] + "\n...[truncated by NewAPI upstream keyword capture]..."
	}

	channelID := 0
	channelName := ""
	channelType := 0
	modelName := ""
	upstreamModelName := ""
	retryIndex := 0
	isStream := false
	requestURLPath := ""
	if info != nil {
		modelName = info.OriginModelName
		upstreamModelName = info.UpstreamModelName
		retryIndex = info.RetryIndex
		isStream = info.IsStream
		requestURLPath = info.RequestURLPath
		if info.ChannelMeta != nil {
			channelID = info.ChannelId
			channelType = info.ChannelType
		}
	}
	if c != nil {
		channelName = c.GetString("channel_name")
		if channelID == 0 {
			channelID = c.GetInt("channel_id")
		}
		if channelType == 0 {
			channelType = c.GetInt("channel_type")
		}
		if requestURLPath == "" && c.Request != nil && c.Request.URL != nil {
			requestURLPath = c.Request.URL.Path
		}
	}
	switchArmed := false
	if channelID > 0 {
		switchArmed = service.ArmUpstreamKeywordCaptureChannelSwitch(channelID, setting)
		if switchArmed {
			service.SetUpstreamKeywordCaptureCurrentRetrySource(c, channelID)
		}
	}

	content := fmt.Sprintf(
		"time=%s\nsource=%s\nrequest_id=%s\nchannel_id=%d\nchannel_name=%s\nchannel_type=%d\nmodel=%s\nupstream_model=%s\nretry_index=%d\nis_stream=%t\nstatus_code=%d\nrequest_path=%s\nmatched_keywords=%s\ntemporary_switch_armed=%t\ntemporary_switch_target_channel_id=%d\ntemporary_switch_count=%d\ntemporary_switch_ttl_seconds=%d\nsha256=%x\n--- payload ---\n%s\n",
		now.Format(time.RFC3339Nano),
		source,
		func() string {
			if c == nil {
				return ""
			}
			return c.GetString(common.RequestIdKey)
		}(),
		channelID,
		channelName,
		channelType,
		modelName,
		upstreamModelName,
		retryIndex,
		isStream,
		statusCode,
		requestURLPath,
		strings.Join(match.Keywords, ","),
		switchArmed,
		setting.UpstreamKeywordCaptureSwitchChannelId,
		service.NormalizeUpstreamKeywordCaptureSwitchCountForLog(setting.UpstreamKeywordCaptureSwitchCount),
		service.NormalizeUpstreamKeywordCaptureSwitchTTLSecondsForLog(setting.UpstreamKeywordCaptureSwitchTTLSeconds),
		sum,
		payload,
	)

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		common.SysError(fmt.Sprintf("failed to capture upstream keyword payload: %v", err))
		return true
	}
	common.SysLog(fmt.Sprintf("captured upstream keyword payload: %s", path))
	return true
}

func CaptureUpstreamBlockedPayload(c *gin.Context, info *relaycommon.RelayInfo, source string, statusCode int, payload string) bool {
	return CaptureUpstreamKeywordPayload(c, info, source, statusCode, payload)
}

func NewUpstreamKeywordCapturedRetryError() *types.NewAPIError {
	return types.NewErrorWithStatusCode(
		errUpstreamKeywordCapturedForRetry,
		types.ErrorCodeUpstreamKeywordCaptured,
		http.StatusServiceUnavailable,
		types.ErrOptionWithNoRecordErrorLog(),
	)
}
