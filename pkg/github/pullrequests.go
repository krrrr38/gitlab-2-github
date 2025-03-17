package github

import (
	"context"
	"fmt"

	githublib "github.com/google/go-github/v60/github"
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
	"github.com/krrrr38/gitlab-2-github/pkg/utils"
)

// PullRequestOptions contains options for creating a pull request
type PullRequestOptions struct {
	Title               string
	Body                string
	Head                string
	Base                string
	Draft               bool
	MaintainerCanModify bool
}

// CreatePullRequest creates a new pull request in GitHub
func CreatePullRequest(ctx context.Context, client *Client, owner, repo string, opts *PullRequestOptions) (*githublib.PullRequest, error) {
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

	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub PR: %w", err)
	}

	logger.Info("Created GitHub PR", "number", pr.GetNumber(), "url", pr.GetHTMLURL())
	return pr, nil
}

// ClosePullRequest closes a pull request
func ClosePullRequest(ctx context.Context, client *Client, owner, repo string, prNumber int) error {
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
		return fmt.Errorf("failed to close GitHub PR: %w", err)
	}

	return nil
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
