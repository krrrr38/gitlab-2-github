package github

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"time"

	"github.com/google/go-github/v60/github"
	"golang.org/x/oauth2"
)

// Client wraps the GitHub client with retry capabilities
type Client struct {
	inner *github.Client
}

// NewClient creates a new GitHub client with the provided token
func NewClient(token string) *Client {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	return &Client{
		inner: github.NewClient(tc),
	}
}

// GetInner returns the underlying GitHub client
func (c *Client) GetInner() *github.Client {
	return c.inner
}

// RetryableOperation retries a GitHub API operation with exponential backoff
func RetryableOperation(ctx context.Context, operation func() error) error {
	var err error
	maxRetries := 5
	backoffFactor := 2.0
	initialDelay := 1 * time.Second
	maxDelay := 60 * time.Second

	for attempt := 0; attempt < maxRetries; attempt++ {
		err = operation()
		if err == nil {
			return nil
		}

		// Check if error is related to rate limit
		if isRateLimitError(err) {
			delay := calculateBackoff(attempt, initialDelay, backoffFactor, maxDelay)
			log.Printf("Rate limit hit. Retrying after %s (attempt %d/%d)", delay, attempt+1, maxRetries)

			// Use context-aware sleep
			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		} else if isRetryableError(err) {
			// Other retryable errors (network issues, 500s, etc.)
			delay := calculateBackoff(attempt, initialDelay, backoffFactor, maxDelay)
			log.Printf("Retryable error: %v. Retrying after %s (attempt %d/%d)", err, delay, attempt+1, maxRetries)

			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		} else {
			// Non-retryable error
			return err
		}
	}

	return fmt.Errorf("operation failed after %d attempts: %w", maxRetries, err)
}

// isRateLimitError determines if an error is due to rate limiting
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	// Check if err is a GitHub error response
	if errResp, ok := err.(*github.ErrorResponse); ok {
		return errResp.Response.StatusCode == http.StatusForbidden && errResp.Message == "API rate limit exceeded"
	}

	return false
}

// isRetryableError determines if an error should be retried
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for GitHub error responses
	if errResp, ok := err.(*github.ErrorResponse); ok {
		code := errResp.Response.StatusCode
		// Retry on server errors (5xx), too many requests (429), and some client errors that might be temporary
		return code == http.StatusTooManyRequests ||
			code == http.StatusInternalServerError ||
			code == http.StatusBadGateway ||
			code == http.StatusServiceUnavailable ||
			code == http.StatusGatewayTimeout
	}

	// Also retry on network/transport errors
	_, isNetError := err.(*url.Error)
	return isNetError
}

// calculateBackoff computes the backoff duration using exponential backoff with jitter
func calculateBackoff(attempt int, initialDelay time.Duration, factor float64, maxDelay time.Duration) time.Duration {
	// Calculate exponential backoff
	backoff := float64(initialDelay) * math.Pow(factor, float64(attempt))

	// Add some jitter (±20%)
	jitter := backoff * 0.2 * (rand.Float64()*2 - 1)
	backoff = backoff + jitter

	// Ensure we don't exceed max delay
	if backoff > float64(maxDelay) {
		backoff = float64(maxDelay)
	}

	return time.Duration(backoff)
}
