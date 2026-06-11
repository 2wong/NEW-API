package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestUpstreamKeywordCaptureChannelSwitchConsumesConfiguredCount(t *testing.T) {
	oldRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	resetUpstreamKeywordCaptureChannelSwitchForTest()
	t.Cleanup(func() {
		common.RedisEnabled = oldRedisEnabled
		resetUpstreamKeywordCaptureChannelSwitchForTest()
	})

	armed := ArmUpstreamKeywordCaptureChannelSwitch(28, dto.ChannelSettings{
		UpstreamKeywordCaptureSwitchEnabled:    true,
		UpstreamKeywordCaptureSwitchChannelId:  15,
		UpstreamKeywordCaptureSwitchCount:      2,
		UpstreamKeywordCaptureSwitchTTLSeconds: 600,
	})

	require.True(t, armed)
	target, ok := ConsumeUpstreamKeywordCaptureChannelSwitch(28)
	require.True(t, ok)
	require.Equal(t, 15, target)
	target, ok = ConsumeUpstreamKeywordCaptureChannelSwitch(28)
	require.True(t, ok)
	require.Equal(t, 15, target)
	_, ok = ConsumeUpstreamKeywordCaptureChannelSwitch(28)
	require.False(t, ok)
}

func TestUpstreamKeywordCaptureChannelSwitchUsesDefaults(t *testing.T) {
	oldRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	resetUpstreamKeywordCaptureChannelSwitchForTest()
	t.Cleanup(func() {
		common.RedisEnabled = oldRedisEnabled
		resetUpstreamKeywordCaptureChannelSwitchForTest()
	})

	armed := ArmUpstreamKeywordCaptureChannelSwitch(28, dto.ChannelSettings{
		UpstreamKeywordCaptureSwitchEnabled:   true,
		UpstreamKeywordCaptureSwitchChannelId: 15,
	})

	require.True(t, armed)
	target, ok := ConsumeUpstreamKeywordCaptureChannelSwitch(28)
	require.True(t, ok)
	require.Equal(t, 15, target)
	_, ok = ConsumeUpstreamKeywordCaptureChannelSwitch(28)
	require.False(t, ok)
}

func TestUpstreamKeywordCaptureChannelSwitchDisabledDoesNotArm(t *testing.T) {
	oldRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	resetUpstreamKeywordCaptureChannelSwitchForTest()
	t.Cleanup(func() {
		common.RedisEnabled = oldRedisEnabled
		resetUpstreamKeywordCaptureChannelSwitchForTest()
	})

	armed := ArmUpstreamKeywordCaptureChannelSwitch(28, dto.ChannelSettings{
		UpstreamKeywordCaptureSwitchEnabled:   false,
		UpstreamKeywordCaptureSwitchChannelId: 15,
	})

	require.False(t, armed)
	_, ok := ConsumeUpstreamKeywordCaptureChannelSwitch(28)
	require.False(t, ok)
}

func TestUpstreamKeywordCaptureChannelSwitchExpires(t *testing.T) {
	oldRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	resetUpstreamKeywordCaptureChannelSwitchForTest()
	t.Cleanup(func() {
		common.RedisEnabled = oldRedisEnabled
		resetUpstreamKeywordCaptureChannelSwitchForTest()
	})

	armed := ArmUpstreamKeywordCaptureChannelSwitch(28, dto.ChannelSettings{
		UpstreamKeywordCaptureSwitchEnabled:    true,
		UpstreamKeywordCaptureSwitchChannelId:  15,
		UpstreamKeywordCaptureSwitchCount:      1,
		UpstreamKeywordCaptureSwitchTTLSeconds: 1,
	})

	require.True(t, armed)
	time.Sleep(1100 * time.Millisecond)
	_, ok := ConsumeUpstreamKeywordCaptureChannelSwitch(28)
	require.False(t, ok)
}
