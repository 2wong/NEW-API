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
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenaiHandlerAcceptsNonStreamEventStreamUsageResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := strings.Join([]string{
		`data: {"id":"","object":"chat.completion.chunk","created":0,"model":"gpt-5.5","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":0,"total_tokens":9}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := newOpenAITestRelayInfo()
	resp := newEventStreamHTTPResponse(body)

	usage, newAPIError := OpenaiHandler(c, info, resp)

	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	require.Equal(t, 9, usage.PromptTokens)
	require.Equal(t, 0, usage.CompletionTokens)
	require.Equal(t, 9, usage.TotalTokens)
	require.NotContains(t, recorder.Body.String(), "data:")
	require.Contains(t, recorder.Header().Get("Content-Type"), "application/json")

	var parsed dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &parsed))
	require.Equal(t, "gpt-5.5", parsed.Model)
	require.Equal(t, 9, parsed.Usage.PromptTokens)
	require.Empty(t, parsed.Choices)
}

func TestOpenaiHandlerAggregatesNonStreamEventStreamContent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := strings.Join([]string{
		`data: {"id":"chatcmpl_sse","object":"chat.completion.chunk","created":1710000000,"model":"gpt-5.5","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl_sse","object":"chat.completion.chunk","created":1710000000,"model":"gpt-5.5","choices":[{"index":0,"delta":{"content":"hello "},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl_sse","object":"chat.completion.chunk","created":1710000000,"model":"gpt-5.5","choices":[{"index":0,"delta":{"content":"world"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl_sse","object":"chat.completion.chunk","created":1710000000,"model":"gpt-5.5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := newOpenAITestRelayInfo()
	resp := newEventStreamHTTPResponse(body)

	usage, newAPIError := OpenaiHandler(c, info, resp)

	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	require.Equal(t, 3, usage.PromptTokens)
	require.Equal(t, 2, usage.CompletionTokens)
	require.Equal(t, 5, usage.TotalTokens)

	var parsed dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &parsed))
	require.Equal(t, "chatcmpl_sse", parsed.Id)
	require.Equal(t, "chat.completion", parsed.Object)
	require.Len(t, parsed.Choices, 1)
	require.Equal(t, "assistant", parsed.Choices[0].Message.Role)
	require.Equal(t, "hello world", parsed.Choices[0].Message.StringContent())
	require.Equal(t, "stop", parsed.Choices[0].FinishReason)
}

func newOpenAITestRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName:    "gpt-5.5",
		RequestURLPath:     "/v1/chat/completions",
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "gpt-5.5",
		},
	}
}

func newEventStreamHTTPResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream; charset=utf-8"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}
