package github

import (
	"context"
	"fmt"

	githublib "github.com/google/go-github/v60/github"
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
	"github.com/krrrr38/gitlab-2-github/pkg/utils"
)

// min returns the smaller of x or y.
func min(x, y int) int {
	if x < y {
		return x
	}
	return y
}

// PullRequestOptions contains options for creating a pull request
type PullRequestOptions struct {
	Title               string
	Body                string
	Head                string
	Base                string
	Draft               bool
	MaintainerCanModify bool
}

// NoDiffError indicates that there's no difference between branches for a PR
type NoDiffError struct {
	Head string
	Base string
}

func (e *NoDiffError) Error() string {
	return fmt.Sprintf("no diff found between branches: %s and %s", e.Head, e.Base)
}

// CreatePullRequest creates a new pull request in GitHub
func CreatePullRequest(ctx context.Context, client *Client, owner, repo string, opts *PullRequestOptions) (*githublib.PullRequest, error) {
	// Log the operation with key parameters
	logger.Debug("Creating GitHub pull request",
		"owner", owner,
		"repo", repo,
		"head", opts.Head,
		"base", opts.Base,
		"title", opts.Title[:min(50, len(opts.Title))]+"...", // Truncate long titles
		"draft", opts.Draft)

	// Create pull request
	newPR := &githublib.NewPullRequest{
		Title:               githublib.String(opts.Title),
		Body:                githublib.String(opts.Body),
		Head:                githublib.String(opts.Head),
		Base:                githublib.String(opts.Base),
		MaintainerCanModify: githublib.Bool(opts.MaintainerCanModify),
		Draft:               githublib.Bool(opts.Draft),
	}

	var pr *githublib.PullRequest
	var err error

	err = RetryableOperation(ctx, func() error {
		pr, _, err = client.GetInner().PullRequests.Create(ctx, owner, repo, newPR)
		return err
	})

	// Log any errors with request parameters
	if err != nil {
		logger.Error("Failed to create GitHub PR",
			"owner", owner,
			"repo", repo,
			"head", opts.Head,
			"base", opts.Base,
			"error", err)
	}

	if err != nil {
		// Check for the specific GitHub error message about no diff between branches
		if errResp, ok := err.(*githublib.ErrorResponse); ok {
			for _, e := range errResp.Errors {
				if e.Message == "No commits between" || e.Message == "At least one commit is required" ||
					e.Message == "No changes between" || e.Message == "There isn't anything to compare" {
					return nil, &NoDiffError{Head: opts.Head, Base: opts.Base}
				}
			}
		}
		return nil, fmt.Errorf("failed to create GitHub PR: %w", err)
	}

	logger.Info("Created GitHub PR", "number", pr.GetNumber(), "url", pr.GetHTMLURL())
	return pr, nil
}

// ClosePullRequest closes a pull request
func ClosePullRequest(ctx context.Context, client *Client, owner, repo string, prNumber int) error {
	// Log the operation with key parameters
	logger.Debug("Closing pull request",
		"owner", owner,
		"repo", repo,
		"prNumber", prNumber)

	// Close the PR with retries
	err := RetryableOperation(ctx, func() error {
		state := "closed"
		closeRequest := &githublib.PullRequest{
			State: &state,
		}
		_, _, err := client.GetInner().PullRequests.Edit(ctx, owner, repo, prNumber, closeRequest)
		return err
	})

	if err != nil {
		logger.Error("Failed to close GitHub PR",
			"owner", owner,
			"repo", repo,
			"prNumber", prNumber,
			"error", err)
		return fmt.Errorf("failed to close GitHub PR: %w", err)
	}

	return nil
}

// DeleteBranch deletes a branch from the repository
func DeleteBranch(ctx context.Context, client *Client, owner, repo, branch string) error {
	// Log the operation with key parameters
	logger.Debug("Deleting branch",
		"owner", owner,
		"repo", repo,
		"branch", branch)

	// Delete the branch with retries
	err := RetryableOperation(ctx, func() error {
		_, err := client.GetInner().Git.DeleteRef(ctx, owner, repo, "refs/heads/"+branch)
		return err
	})

	if err != nil {
		logger.Error("Failed to delete branch",
			"owner", owner,
			"repo", repo,
			"branch", branch,
			"error", err)
		return fmt.Errorf("failed to delete branch %s: %w", branch, err)
	}

	logger.Info("Deleted branch", "branch", branch)
	return nil
}

// BranchExists checks if a branch exists in the repository
func BranchExists(ctx context.Context, client *Client, owner, repo, branch string) (bool, error) {
	// Log the operation with key parameters
	logger.Debug("Checking if branch exists",
		"owner", owner,
		"repo", repo,
		"branch", branch)

	var exists bool
	err := RetryableOperation(ctx, func() error {
		_, resp, err := client.GetInner().Repositories.GetBranch(ctx, owner, repo, branch, 0)
		if err != nil {
			if resp != nil && resp.StatusCode == 404 {
				exists = false
				return nil
			}
			return err
		}
		exists = true
		return nil
	})

	if err != nil {
		logger.Error("Failed to check if branch exists",
			"owner", owner,
			"repo", repo,
			"branch", branch,
			"error", err)
		return false, fmt.Errorf("failed to check if branch exists: %w", err)
	}

	return exists, nil
}

// CreatePRComment creates a regular (non-review) comment on a pull request
func CreatePRComment(ctx context.Context, client *Client, owner, repo string, prNumber int, body string) error {
	// 文字数制限に合わせて切り詰める
	truncatedBody := utils.TruncateText(body, utils.MaxCommentLength)

	return RetryableOperation(ctx, func() error {
		_, _, err := client.GetInner().Issues.CreateComment(ctx, owner, repo, prNumber,
			&githublib.IssueComment{Body: &truncatedBody})
		return err
	})
}

// CreatePRReviewComment creates a review comment on a specific line and file
func CreatePRReviewComment(ctx context.Context, client *Client, owner, repo string, prNumber int, body, path string, position int) (*githublib.PullRequestComment, error) {
	// 文字数制限に合わせて切り詰める
	truncatedBody := utils.TruncateText(body, utils.MaxCommentLength)

	var comment *githublib.PullRequestComment

	err := RetryableOperation(ctx, func() error {
		reviewComment := &githublib.PullRequestComment{
			Body:     &truncatedBody,
			Path:     &path,
			Position: githublib.Int(position),
		}

		var err error
		comment, _, err = client.GetInner().PullRequests.CreateComment(ctx, owner, repo, prNumber, reviewComment)
		return err
	})

	if err != nil {
		return nil, err
	}

	return comment, nil
}

// CreatePRReviewCommentReply creates a reply to an existing review comment
func CreatePRReviewCommentReply(ctx context.Context, client *Client, owner, repo string, prNumber int, body string, inReplyTo int64) (*githublib.PullRequestComment, error) {
	// 文字数制限に合わせて切り詰める
	truncatedBody := utils.TruncateText(body, utils.MaxCommentLength)

	var comment *githublib.PullRequestComment

	err := RetryableOperation(ctx, func() error {
		reviewComment := &githublib.PullRequestComment{
			Body:      &truncatedBody,
			InReplyTo: githublib.Int64(inReplyTo),
		}

		var err error
		comment, _, err = client.GetInner().PullRequests.CreateComment(ctx, owner, repo, prNumber, reviewComment)
		return err
	})

	if err != nil {
		return nil, err
	}

	return comment, nil
}
