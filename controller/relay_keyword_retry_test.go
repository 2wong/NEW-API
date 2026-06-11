package controller

import (
	"errors"
	"net/http"
	"testing"

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
