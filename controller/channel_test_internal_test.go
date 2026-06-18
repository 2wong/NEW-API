package controller

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSettleTestQuotaUsesTieredBilling(t *testing.T) {
	info := &relaycommon.RelayInfo{
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:   "tiered_expr",
			ExprString:    `param("stream") == true ? tier("stream", p * 3) : tier("base", p * 2)`,
			ExprHash:      billingexpr.ExprHashString(`param("stream") == true ? tier("stream", p * 3) : tier("base", p * 2)`),
			GroupRatio:    1,
			EstimatedTier: "stream",
			QuotaPerUnit:  common.QuotaPerUnit,
			ExprVersion:   1,
		},
		BillingRequestInput: &billingexpr.RequestInput{
			Body: []byte(`{"stream":true}`),
		},
	}

	quota, result := settleTestQuota(info, types.PriceData{
		ModelRatio:      1,
		CompletionRatio: 2,
	}, &dto.Usage{
		PromptTokens: 1000,
	})

	require.Equal(t, 1500, quota)
	require.NotNil(t, result)
	require.Equal(t, "stream", result.MatchedTier)
}

func TestBuildTestLogOtherInjectsTieredInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	info := &relaycommon.RelayInfo{
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode: "tiered_expr",
			ExprString:  `tier("base", p * 2)`,
		},
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	priceData := types.PriceData{
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	}
	usage := &dto.Usage{
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 12,
		},
	}

	other := buildTestLogOther(ctx, info, priceData, usage, &billingexpr.TieredResult{
		MatchedTier: "base",
	})

	require.Equal(t, "tiered_expr", other["billing_mode"])
	require.Equal(t, "base", other["matched_tier"])
	require.NotEmpty(t, other["expr_b64"])
}

func setupChannelTestStatusRetryDB(t *testing.T) *gorm.DB {
	t.Helper()

	gin.SetMode(gin.TestMode)
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
	})

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestNextStatusCodeRetryChannelForChannelTestDefaultsToSource(t *testing.T) {
	db := setupChannelTestStatusRetryDB(t)
	setting := dto.ChannelSettings{
		StatusCodeRetryEnabled:     true,
		StatusCodeRetryStatusCodes: "429",
		StatusCodeRetryCount:       1,
		StatusCodeRetryChannelId:   0,
	}
	source := &model.Channel{
		Id:     28,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "sk-test",
		Name:   "source",
		Status: common.ChannelStatusEnabled,
	}
	source.SetSetting(setting)
	require.NoError(t, db.Create(source).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	next, ok := nextStatusCodeRetryChannelForChannelTest(ctx, source, types.InitOpenAIError(types.ErrorCodeBadResponseStatusCode, 429), "gpt-4o-mini", map[string]int{})

	require.True(t, ok)
	require.Equal(t, source.Id, next.Id)
}

func TestNextStatusCodeRetryChannelForChannelTestHonorsRetryCount(t *testing.T) {
	db := setupChannelTestStatusRetryDB(t)
	setting := dto.ChannelSettings{
		StatusCodeRetryEnabled:     true,
		StatusCodeRetryStatusCodes: "429",
		StatusCodeRetryCount:       1,
	}
	source := &model.Channel{
		Id:     28,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "sk-test",
		Name:   "source",
		Status: common.ChannelStatusEnabled,
	}
	source.SetSetting(setting)
	require.NoError(t, db.Create(source).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	attempts := map[string]int{}
	_, ok := nextStatusCodeRetryChannelForChannelTest(ctx, source, types.InitOpenAIError(types.ErrorCodeBadResponseStatusCode, 429), "gpt-4o-mini", attempts)
	require.True(t, ok)

	_, ok = nextStatusCodeRetryChannelForChannelTest(ctx, source, types.InitOpenAIError(types.ErrorCodeBadResponseStatusCode, 429), "gpt-4o-mini", attempts)
	require.False(t, ok)
}
