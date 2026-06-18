package controller

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestShouldRetryUpstreamKeywordCapturedIgnoresRetryBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	err := types.NewErrorWithStatusCode(
		errors.New("upstream keyword captured; retry current request with switched channel"),
		types.ErrorCodeUpstreamKeywordCaptured,
		http.StatusServiceUnavailable,
	)

	require.True(t, shouldRetry(c, err, 0))
}

func TestGetChannelWithoutChannelMetaLoadsFullChannelSettings(t *testing.T) {
	db := setupChannelTestStatusRetryDB(t)
	setting := dto.ChannelSettings{
		StatusCodeRetryEnabled:     true,
		StatusCodeRetryStatusCodes: "503",
		StatusCodeRetryCount:       2,
	}
	channel := &model.Channel{
		Id:      28,
		Type:    constant.ChannelTypeOpenAI,
		Key:     "sk-test",
		Name:    "source",
		Status:  common.ChannelStatusEnabled,
		AutoBan: common.GetPointer(1),
	}
	channel.SetSetting(setting)
	require.NoError(t, db.Create(channel).Error)

	c, _ := gin.CreateTestContext(nil)
	c.Set("channel_id", 28)
	c.Set("channel_type", constant.ChannelTypeOpenAI)
	c.Set("channel_name", "source")
	got, err := getChannel(c, &relaycommon.RelayInfo{OriginModelName: "gpt-5.5"}, &service.RetryParam{})

	require.Nil(t, err)
	require.Equal(t, 28, got.Id)
	require.True(t, got.GetSetting().StatusCodeRetryEnabled)
	require.Equal(t, "503", got.GetSetting().StatusCodeRetryStatusCodes)
}

func TestGetChannelAppliesPendingStatusCodeRetryWithoutChannelMeta(t *testing.T) {
	db := setupChannelTestStatusRetryDB(t)
	channel := &model.Channel{
		Id:      28,
		Type:    constant.ChannelTypeOpenAI,
		Key:     "sk-test",
		Name:    "source",
		Status:  common.ChannelStatusEnabled,
		AutoBan: common.GetPointer(1),
	}
	require.NoError(t, db.Create(channel).Error)

	c, _ := gin.CreateTestContext(nil)
	c.Set("channel_id", 28)
	c.Set("channel_type", constant.ChannelTypeOpenAI)
	c.Set("channel_name", "source")
	service.SetPendingStatusCodeRetryForTest(c, 28, 28, http.StatusServiceUnavailable)
	got, err := getChannel(c, &relaycommon.RelayInfo{OriginModelName: "gpt-5.5"}, &service.RetryParam{})

	require.Nil(t, err)
	require.Equal(t, 28, got.Id)
	require.False(t, service.HasPendingStatusCodeRetry(c))
}

func TestShouldRetryPendingStatusCodeRetryIgnoresRetryBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	service.SetPendingStatusCodeRetryForTest(c, 28, 28, http.StatusTooManyRequests)
	err := types.NewErrorWithStatusCode(
		errors.New("upstream rate limited"),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusTooManyRequests,
	)

	require.True(t, shouldRetry(c, err, 0))
}
