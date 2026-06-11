package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOaiResponsesStreamHandler_RetriesCurrentRequestBeforeWritingBlockedCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldLogDir := common.LogDir
	oldRedisEnabled := common.RedisEnabled
	oldStreamingTimeout := constant.StreamingTimeout
	logDir := t.TempDir()
	common.LogDir = &logDir
	common.RedisEnabled = false
	constant.StreamingTimeout = 30
	service.ResetUpstreamKeywordCaptureChannelSwitchForTest()
	t.Cleanup(func() {
		common.LogDir = oldLogDir
		common.RedisEnabled = oldRedisEnabled
		constant.StreamingTimeout = oldStreamingTimeout
		service.ResetUpstreamKeywordCaptureChannelSwitchForTest()
	})

	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1"}}`,
		`data: {"type":"response.output_text.done","text":"REQUEST_BLOCKED\n\nCategory: REVERSE_ENGINEERING\n\nReason: Restricted technical activity detected."}`,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":22,"total_tokens":32}}}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.5",
		RequestURLPath:  "/v1/responses",
		IsStream:        true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         28,
			ChannelType:       1,
			UpstreamModelName: "gpt-5.5",
			ChannelSetting: dto.ChannelSettings{
				UpstreamKeywordCaptureEnabled:         true,
				UpstreamKeywordCaptureKeywords:        []string{"REQUEST_BLOCKED"},
				UpstreamKeywordCaptureSwitchEnabled:   true,
				UpstreamKeywordCaptureSwitchChannelId: 15,
				UpstreamKeywordCaptureSwitchCount:     2,
			},
		},
	}
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}

	usage, newAPIError := OaiResponsesStreamHandler(c, info, resp)

	require.Nil(t, usage)
	require.True(t, types.IsUpstreamKeywordCapturedError(newAPIError))
	require.NotContains(t, recorder.Body.String(), "REQUEST_BLOCKED")
	source, ok := service.GetUpstreamKeywordCaptureCurrentRetrySourceForTest(c)
	require.True(t, ok)
	require.Equal(t, 28, source)
}
