package domain

import (
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/sdk_error"
)

// RateLimitError indicates the VngCloud API returned HTTP 429 (Too Many
// Requests). RetryAfter is the duration the server asked us to wait before
// retrying, parsed from X-Ratelimit-Reset (seconds) or Retry-After. It is
// zero when the server gave no usable hint; callers should fall back to a
// sane default in that case.
type RateLimitError struct {
	RetryAfter time.Duration
	URL        string
	Method     string
	cause      error
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("vngcloud rate limit exceeded (retry after %s) on %s %s: %v",
		e.RetryAfter, e.Method, e.URL, e.cause)
}

func (e *RateLimitError) Unwrap() error { return e.cause }

// SDKError converts an SDK error into a Go error. When the underlying
// response is HTTP 429 it returns *RateLimitError; otherwise it falls
// through to sdkErr.GetError(). Repository code should use this in place of
// calling GetError() directly so 429s are visible to the reconcile layer.
func SDKError(sdkErr sdk_error.IError) error {
	if sdkErr == nil {
		return nil
	}
	if rl := parseRateLimit(sdkErr); rl != nil {
		return rl
	}
	return sdkErr.GetError()
}

func parseRateLimit(sdkErr sdk_error.IError) *RateLimitError {
	params := sdkErr.GetParameters()
	code, _ := params["statusCode"].(int)
	if code != http.StatusTooManyRequests {
		return nil
	}
	headers, _ := params["responseHeaders"].(http.Header)
	method, _ := params["method"].(string)
	url, _ := params["url"].(string)
	return &RateLimitError{
		RetryAfter: parseRetryAfter(headers),
		URL:        url,
		Method:     method,
		cause:      sdkErr.GetError(),
	}
}

func parseRetryAfter(h http.Header) time.Duration {
	if h == nil {
		return 0
	}
	if v := h.Get("X-Ratelimit-Reset"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
	}
	if v := h.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
		if t, err := http.ParseTime(v); err == nil {
			if d := time.Until(t); d > 0 {
				return d
			}
		}
	}
	return 0
}

// RateLimitRequeueAfter returns the duration a reconcile should wait before
// retrying after a 429. It enforces a 2s floor (so a 0 or sub-second hint
// from the server still gives the bucket time to refill) and a 5m ceiling
// (so a buggy server can't park a workqueue item for hours), then adds up
// to 50% jitter so independent processes don't retry in lockstep.
func RateLimitRequeueAfter(d time.Duration) time.Duration {
	const minWait = 2 * time.Second
	const maxWait = 5 * time.Minute
	if d < minWait {
		d = minWait
	}
	if d > maxWait {
		d = maxWait
	}
	jitter := time.Duration(rand.Int63n(int64(d / 2)))
	return d + jitter
}
