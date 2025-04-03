package github

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v70/github"
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"
)

// Client wraps the GitHub client with retry capabilities
type Client struct {
	inner *github.Client
	v4    *githubv4.Client
}

// NewClientByPAT creates a new GitHub client with the provided token
func NewClientByPAT(token string) *Client {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	return &Client{
		inner: github.NewClient(tc),
		v4:    githubv4.NewClient(tc),
	}
}

func NewClientByApp(appID, installationID int, privateKey string) *Client {
	itr, err := ghinstallation.New(http.DefaultTransport, int64(appID), int64(installationID), []byte(privateKey))
	if err != nil {
		logger.Fatal("failed to create gh client", "error", err)
	}
	return &Client{
		inner: github.NewClient(&http.Client{Transport: itr}),
		v4:    githubv4.NewClient(&http.Client{Transport: itr}),
	}
}

// GetInner returns the underlying GitHub client
func (client *Client) GetInner() *github.Client {
	return client.inner
}

// GetV4 returns the underlying GitHub GraphQL client
func (client *Client) GetV4() *githubv4.Client {
	return client.v4
}

// DeleteRepository deletes a GitHub repository
func DeleteRepository(ctx context.Context, client *Client, owner, repo string) error {
	logger.Debug("Deleting GitHub repository", "owner", owner, "repo", repo)

	err := RetryableOperation(ctx, func() error {
		_, err := client.GetInner().Repositories.Delete(ctx, owner, repo)
		return err
	})

	if err != nil {
		logger.Error("Failed to delete GitHub repository", "owner", owner, "repo", repo, "error", err)
		return fmt.Errorf("failed to delete GitHub repository: %w", err)
	}

	logger.Debug("Successfully deleted GitHub repository", "owner", owner, "repo", repo)
	return nil
}

// CreateRepository creates an empty GitHub repository
func CreateRepository(ctx context.Context, client *Client, owner, repo, description string, url *url.URL) error {
	logger.Debug("Creating GitHub repository", "owner", owner, "repo", repo, "url", url)

	ownerDetail, _, err := client.GetInner().Users.Get(ctx, owner)
	if err != nil {
		return fmt.Errorf("failed to get owner detail: %w", err)
	}

	// visibility=Internal とするためにRESTAPIではなくgraphql APIを利用
	var mutation struct {
		CreateRepository struct {
			Repository struct {
				ID    githubv4.ID
				Name  githubv4.String
				Owner struct {
					Login githubv4.String
				}
			}
		} `graphql:"createRepository(input: $input)"`
	}
	input := githubv4.CreateRepositoryInput{
		Name:           githubv4.String(repo),
		Visibility:     githubv4.RepositoryVisibilityInternal,
		OwnerID:        githubv4.NewID(ownerDetail.GetNodeID()),
		Description:    githubv4.NewString(githubv4.String(description)),
		HasWikiEnabled: githubv4.NewBoolean(false),
		HomepageURL: githubv4.NewURI(githubv4.URI{
			URL: url,
		}),
	}
	err = RetryableOperation(ctx, func() error {
		return client.GetV4().Mutate(ctx, &mutation, input, nil)
	})
	if err != nil {
		logger.Error("Failed to create GitHub repository", "owner", owner, "repo", repo, "error", err)
		return fmt.Errorf("failed to create GitHub repository: %w", err)
	}

	logger.Debug("Successfully created GitHub repository", "owner", owner, "repo", repo)
	return nil
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
			return fmt.Errorf("rate limited: %w", err)
		} else if isRetryableError(err) {
			// Other retryable errors (network issues, 500s, etc.)
			delay := calculateBackoff(attempt, initialDelay, backoffFactor, maxDelay)
			logger.Info(fmt.Sprintf("Retryable error: %v. Retrying after %s (attempt %d/%d)", err, delay, attempt+1, maxRetries))

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
		statusCode := errResp.Response.StatusCode
		return (statusCode == http.StatusForbidden && errResp.Message == "rate limit") || statusCode == http.StatusTooManyRequests
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
