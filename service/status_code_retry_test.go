package service

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestStatusCodeRetryMatchesConfiguredStatusRanges(t *testing.T) {
	setting := dto.ChannelSettings{
		StatusCodeRetryEnabled:     true,
		StatusCodeRetryStatusCodes: "429,500-503",
	}

	require.True(t, ShouldStatusCodeRetry(setting, 429))
	require.True(t, ShouldStatusCodeRetry(setting, 500))
	require.True(t, ShouldStatusCodeRetry(setting, 503))
	require.False(t, ShouldStatusCodeRetry(setting, 404))
	require.False(t, ShouldStatusCodeRetry(setting, 504))
}

func TestStatusCodeRetryRequiresEnabledAndValidCodes(t *testing.T) {
	require.False(t, ShouldStatusCodeRetry(dto.ChannelSettings{
		StatusCodeRetryEnabled:     false,
		StatusCodeRetryStatusCodes: "429",
	}, 429))
	require.False(t, ShouldStatusCodeRetry(dto.ChannelSettings{
		StatusCodeRetryEnabled:     true,
		StatusCodeRetryStatusCodes: "",
	}, 429))
	require.False(t, ShouldStatusCodeRetry(dto.ChannelSettings{
		StatusCodeRetryEnabled:     true,
		StatusCodeRetryStatusCodes: "not-a-code",
	}, 429))
}

func TestConsumeStatusCodeRetryAttemptHonorsConfiguredCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	setting := dto.ChannelSettings{
		StatusCodeRetryEnabled:     true,
		StatusCodeRetryStatusCodes: "429",
		StatusCodeRetryCount:       2,
	}

	require.True(t, ConsumeStatusCodeRetryAttempt(c, 28, 429, setting))
	require.True(t, ConsumeStatusCodeRetryAttempt(c, 28, 429, setting))
	require.False(t, ConsumeStatusCodeRetryAttempt(c, 28, 429, setting))
}

func TestStatusCodeRetryTargetDefaultsToSourceChannel(t *testing.T) {
	require.Equal(t, 28, ResolveStatusCodeRetryTargetChannelID(28, dto.ChannelSettings{
		StatusCodeRetryChannelId: 0,
	}))
	require.Equal(t, 15, ResolveStatusCodeRetryTargetChannelID(28, dto.ChannelSettings{
		StatusCodeRetryChannelId: 15,
	}))
}
