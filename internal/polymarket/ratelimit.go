package polymarket

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"time"

	"golang.org/x/time/rate"
)

const (
	defaultInitialBackoffMs = 100
	defaultMaxBackoffMs     = 30000
	defaultMaxRetries       = 5
)

// RetryConfig holds retry and backoff configuration
type RetryConfig struct {
	MaxRetries       int           // Maximum retry attempts (default: 5)
	InitialBackoffMs int64         // Initial backoff in milliseconds (default: 100)
	MaxBackoffMs     int64         // Maximum backoff in milliseconds (default: 30000)
	Timeout          time.Duration // Per-request timeout (default: 10s)
}

// DefaultRetryConfig returns conservative retry settings
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:       defaultMaxRetries,
		InitialBackoffMs: defaultInitialBackoffMs,
		MaxBackoffMs:     defaultMaxBackoffMs,
		Timeout:          10 * time.Second,
	}
}

// RateLimitedClient wraps http.Client with rate limiting and retry logic
type RateLimitedClient struct {
	httpClient *http.Client
	limiter    *rate.Limiter
	retryConfig RetryConfig
	logger     *slog.Logger
}

// NewRateLimitedClient creates a client with rate limiting (requests per second) and retry logic
func NewRateLimitedClient(requestsPerSecond float64, retryConfig RetryConfig, logger *slog.Logger) *RateLimitedClient {
	if logger == nil {
		logger = slog.Default()
	}
	if retryConfig.MaxRetries == 0 {
		retryConfig = DefaultRetryConfig()
	}

	return &RateLimitedClient{
		httpClient: &http.Client{
			Timeout: retryConfig.Timeout,
		},
		limiter:     rate.NewLimiter(rate.Limit(requestsPerSecond), 1),
		retryConfig: retryConfig,
		logger:      logger,
	}
}

// Do executes an HTTP request with rate limiting and exponential backoff on 429
// Retries on 429 (Too Many Requests) with exponential backoff + jitter
// Returns the response or error after max retries exhausted
func (rc *RateLimitedClient) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	backoffMs := rc.retryConfig.InitialBackoffMs

	for attempt := 0; attempt < rc.retryConfig.MaxRetries; attempt++ {
		// Wait for rate limit token
		if err := rc.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limiter error: %w", err)
		}

		// Execute request
		resp, err := rc.httpClient.Do(req)
		if err != nil {
			rc.logger.Error("request failed",
				slog.String("url", req.URL.String()),
				slog.String("error", err.Error()),
				slog.Int("attempt", attempt+1))
			return nil, fmt.Errorf("request failed: %w", err)
		}

		// Handle rate limiting (429) with exponential backoff
		if resp.StatusCode == http.StatusTooManyRequests {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if attempt < rc.retryConfig.MaxRetries-1 {
				// Add jitter: backoff * (0.5 + rand(0.5))
				jitter := time.Duration(backoffMs/2 + rand.Int63n(backoffMs/2))
				rc.logger.Warn("rate limited, backing off",
					slog.Int64("backoff_ms", backoffMs),
					slog.Int("attempt", attempt+1),
					slog.String("url", req.URL.String()))

				time.Sleep(jitter * time.Millisecond)

				// Exponential backoff: double the backoff, cap at maxBackoffMs
				backoffMs = int64(math.Min(float64(backoffMs*2), float64(rc.retryConfig.MaxBackoffMs)))
				continue
			}

			return nil, fmt.Errorf("rate limited after %d retries (last body: %s)", rc.retryConfig.MaxRetries, string(body))
		}

		// Success or non-429 error — return as-is
		return resp, nil
	}

	return nil, fmt.Errorf("max retries exceeded")
}
