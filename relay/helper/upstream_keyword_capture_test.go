package helper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMatchUpstreamKeywordCapturePayload_DisabledDoesNotMatch(t *testing.T) {
	setting := dto.ChannelSettings{
		UpstreamKeywordCaptureEnabled:  false,
		UpstreamKeywordCaptureKeywords: []string{"cyber_policy"},
	}

	match := MatchUpstreamKeywordCapturePayload(setting, `data: {"type":"error","code":"cyber_policy"}`)

	require.False(t, match.Matched)
	require.Empty(t, match.Keywords)
}

func TestMatchUpstreamKeywordCapturePayload_EnabledMatchesKeyword(t *testing.T) {
	setting := dto.ChannelSettings{
		UpstreamKeywordCaptureEnabled:  true,
		UpstreamKeywordCaptureKeywords: []string{"cyber_policy", "REQUEST_BLOCKED"},
	}

	match := MatchUpstreamKeywordCapturePayload(setting, `data: {"type":"error","code":"cyber_policy","message":"blocked"}`)

	require.True(t, match.Matched)
	require.Equal(t, []string{"cyber_policy"}, match.Keywords)
}

func TestMatchUpstreamKeywordCapturePayload_CaseInsensitive(t *testing.T) {
	setting := dto.ChannelSettings{
		UpstreamKeywordCaptureEnabled:  true,
		UpstreamKeywordCaptureKeywords: []string{"trusted access for cyber"},
	}

	match := MatchUpstreamKeywordCapturePayload(setting, `data: {"message":"Join the Trusted Access For Cyber program"}`)

	require.True(t, match.Matched)
	require.Equal(t, []string{"trusted access for cyber"}, match.Keywords)
}

func TestMatchUpstreamKeywordCapturePayload_IgnoresResponsesInstructionEcho(t *testing.T) {
	setting := dto.ChannelSettings{
		UpstreamKeywordCaptureEnabled:  true,
		UpstreamKeywordCaptureKeywords: []string{"REQUEST_BLOCKED"},
	}
	payload := `data: {"type":"response.created","response":{"instructions":"If upstream returns REQUEST_BLOCKED then capture it"}}`

	match := MatchUpstreamKeywordCapturePayload(setting, payload)

	require.False(t, match.Matched)
	require.Empty(t, match.Keywords)
}

func TestMatchUpstreamKeywordCapturePayload_IgnoresFunctionCallArgumentEcho(t *testing.T) {
	setting := dto.ChannelSettings{
		UpstreamKeywordCaptureEnabled:  true,
		UpstreamKeywordCaptureKeywords: []string{"REQUEST_BLOCKED"},
	}
	payload := `data: {"type":"response.function_call_arguments.delta","delta":"grep -R REQUEST_BLOCKED logs"}`

	match := MatchUpstreamKeywordCapturePayload(setting, payload)

	require.False(t, match.Matched)
	require.Empty(t, match.Keywords)
}

func TestMatchUpstreamKeywordCapturePayload_MatchesRequestBlockedTextAtStart(t *testing.T) {
	setting := dto.ChannelSettings{
		UpstreamKeywordCaptureEnabled:  true,
		UpstreamKeywordCaptureKeywords: []string{"REQUEST_BLOCKED"},
	}
	payload := `data: {"type":"response.output_text.done","text":"REQUEST_BLOCKED\n\nCategory: REVERSE_ENGINEERING\n\nReason: Restricted technical activity detected."}`

	match := MatchUpstreamKeywordCapturePayload(setting, payload)

	require.True(t, match.Matched)
	require.Equal(t, []string{"REQUEST_BLOCKED"}, match.Keywords)
}

func TestMatchUpstreamKeywordCapturePayload_IgnoresRequestBlockedMentionInNormalText(t *testing.T) {
	setting := dto.ChannelSettings{
		UpstreamKeywordCaptureEnabled:  true,
		UpstreamKeywordCaptureKeywords: []string{"REQUEST_BLOCKED"},
	}
	payload := `data: {"type":"response.output_text.done","text":"我在排查 REQUEST_BLOCKED 对应的渠道切换日志"}`

	match := MatchUpstreamKeywordCapturePayload(setting, payload)

	require.False(t, match.Matched)
	require.Empty(t, match.Keywords)
}

func TestCaptureUpstreamKeywordPayload_WritesMatchedKeywordLog(t *testing.T) {
	logDir := t.TempDir()
	oldLogDir := common.LogDir
	oldRedisEnabled := common.RedisEnabled
	common.LogDir = &logDir
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.LogDir = oldLogDir
		common.RedisEnabled = oldRedisEnabled
	})

	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.5",
		RequestURLPath:  "/v1/responses",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:   28,
			ChannelType: 1,
			ChannelSetting: dto.ChannelSettings{
				UpstreamKeywordCaptureEnabled:          true,
				UpstreamKeywordCaptureKeywords:         []string{"cyber_policy"},
				UpstreamKeywordCaptureSwitchEnabled:    true,
				UpstreamKeywordCaptureSwitchChannelId:  15,
				UpstreamKeywordCaptureSwitchCount:      1,
				UpstreamKeywordCaptureSwitchTTLSeconds: 600,
			},
		},
	}

	captured := CaptureUpstreamKeywordPayload(nil, info, "test_raw_body", 200, `data: {"type":"error","code":"cyber_policy"}`)

	require.True(t, captured)
	files, err := filepath.Glob(filepath.Join(logDir, "upstream-keyword-hit-*.log"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	contentBytes, err := os.ReadFile(files[0])
	require.NoError(t, err)
	content := string(contentBytes)
	require.Contains(t, content, "matched_keywords=cyber_policy")
	require.Contains(t, content, "channel_id=28")
	require.Contains(t, content, "request_path=/v1/responses")
	require.Contains(t, content, "temporary_switch_armed=true")
	require.Contains(t, content, "temporary_switch_target_channel_id=15")
	require.True(t, strings.Contains(content, `"code":"cyber_policy"`))
	target, ok := service.ConsumeUpstreamKeywordCaptureChannelSwitch(28)
	require.True(t, ok)
	require.Equal(t, 15, target)
}

func TestCaptureUpstreamKeywordPayload_ArmsCurrentRequestReplaySource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logDir := t.TempDir()
	oldLogDir := common.LogDir
	oldRedisEnabled := common.RedisEnabled
	common.LogDir = &logDir
	common.RedisEnabled = false
	service.ResetUpstreamKeywordCaptureChannelSwitchForTest()
	t.Cleanup(func() {
		common.LogDir = oldLogDir
		common.RedisEnabled = oldRedisEnabled
		service.ResetUpstreamKeywordCaptureChannelSwitchForTest()
	})

	c, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.5",
		RequestURLPath:  "/v1/responses",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:   28,
			ChannelType: 1,
			ChannelSetting: dto.ChannelSettings{
				UpstreamKeywordCaptureEnabled:         true,
				UpstreamKeywordCaptureKeywords:        []string{"REQUEST_BLOCKED"},
				UpstreamKeywordCaptureSwitchEnabled:   true,
				UpstreamKeywordCaptureSwitchChannelId: 15,
				UpstreamKeywordCaptureSwitchCount:     2,
			},
		},
	}

	captured := CaptureUpstreamKeywordPayload(c, info, "test_stream_line", 200, `data: {"type":"response.output_text.done","text":"REQUEST_BLOCKED\n\nCategory: REVERSE_ENGINEERING"}`)

	require.True(t, captured)
	source, ok := service.GetUpstreamKeywordCaptureCurrentRetrySourceForTest(c)
	require.True(t, ok)
	require.Equal(t, 28, source)
	target, ok := service.ConsumeUpstreamKeywordCaptureChannelSwitch(28)
	require.True(t, ok)
	require.Equal(t, 15, target)
}
