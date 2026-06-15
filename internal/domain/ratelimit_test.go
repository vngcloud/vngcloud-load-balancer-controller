package domain

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/sdk_error"
)

func makeSDKErr(statusCode int, headers http.Header) sdk_error.IError {
	return sdk_error.ErrorHandler(errors.New("upstream rejected")).WithKVparameters(
		"statusCode", statusCode,
		"url", "https://example/api",
		"method", "GET",
		"responseHeaders", headers,
	)
}

func TestSDKError_NotRateLimit(t *testing.T) {
	sdkErr := makeSDKErr(http.StatusInternalServerError, nil)

	err := SDKError(sdkErr)

	assert.False(t, IsRateLimitExceeded(err))
	var rl *RateLimitError
	assert.False(t, errors.As(err, &rl))
}

func TestSDKError_RateLimitWithResetHeader(t *testing.T) {
	headers := http.Header{
		"X-Ratelimit-Reset": []string{"7"},
	}
	sdkErr := makeSDKErr(http.StatusTooManyRequests, headers)

	err := SDKError(sdkErr)

	assert.True(t, IsRateLimitExceeded(err))
	var rl *RateLimitError
	assert.True(t, errors.As(err, &rl))
	assert.Equal(t, 7*time.Second, rl.RetryAfter)
	assert.Equal(t, "GET", rl.Method)
}

func TestSDKError_RateLimitWithRetryAfterFallback(t *testing.T) {
	headers := http.Header{
		"Retry-After": []string{"42"},
	}
	sdkErr := makeSDKErr(http.StatusTooManyRequests, headers)

	rl := &RateLimitError{}
	assert.True(t, errors.As(SDKError(sdkErr), &rl))
	assert.Equal(t, 42*time.Second, rl.RetryAfter)
}

func TestSDKError_RateLimitNoHeader(t *testing.T) {
	sdkErr := makeSDKErr(http.StatusTooManyRequests, http.Header{})

	rl := &RateLimitError{}
	assert.True(t, errors.As(SDKError(sdkErr), &rl))
	assert.Zero(t, rl.RetryAfter)
}

func TestSDKError_Nil(t *testing.T) {
	assert.NoError(t, SDKError(nil))
}

func TestRateLimitRequeueAfter_FloorAndJitter(t *testing.T) {
	for range 50 {
		got := RateLimitRequeueAfter(0)
		assert.GreaterOrEqual(t, got, 2*time.Second, "below floor")
		assert.Less(t, got, 3*time.Second, "above floor + max jitter (50%%)")
	}
}

func TestRateLimitRequeueAfter_RespectsServerHint(t *testing.T) {
	for range 50 {
		got := RateLimitRequeueAfter(10 * time.Second)
		assert.GreaterOrEqual(t, got, 10*time.Second)
		assert.Less(t, got, 15*time.Second)
	}
}

func TestRateLimitRequeueAfter_Ceiling(t *testing.T) {
	got := RateLimitRequeueAfter(1 * time.Hour)
	// 5m ceiling + up to 50% jitter
	assert.GreaterOrEqual(t, got, 5*time.Minute)
	assert.Less(t, got, 8*time.Minute)
}
