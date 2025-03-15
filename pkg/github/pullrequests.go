package github

import (
	"context"
	"fmt"

	githublib "github.com/google/go-github/v60/github"
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
	"github.com/krrrr38/gitlab-2-github/pkg/utils"
	"github.com/xanzy/go-gitlab"
)

// CreatePullRequest creates a new pull request in GitHub
func CreatePullRequest(ctx context.Context, client *Client, owner, repo, title, body, head, base string, mr *gitlab.MergeRequest) (*githublib.PullRequest, error) {
	// タイトルはすでに切り詰め済みと想定します

	// Create the PR with retries
	var pr *githublib.PullRequest
	
	err := RetryableOperation(ctx, func() error {
		var err error
		newPR := &githublib.NewPullRequest{
			Title:               &title,
			Body:                &body,
			Head:                &head,
			Base:                &base,
			MaintainerCanModify: githublib.Bool(true),
		}
		
		pr, _, err = client.GetInner().PullRequests.Create(ctx, owner, repo, newPR)
		return err
	})
	
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub PR: %w", err)
	}

	logger.Info("Created GitHub PR", "number", pr.GetNumber())
	return pr, nil
}

// ClosePullRequest closes a pull request and optionally adds a merge comment
func ClosePullRequest(ctx context.Context, client *Client, owner, repo string, prNumber int, wasMerged bool, mrIID int) error {
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
	
	if wasMerged {
		// Add a comment indicating it was merged in GitLab
		comment := fmt.Sprintf("This PR was merged in GitLab as MR #%d", mrIID)
		// コメントの長さは十分短いため切り詰めは不要
		
		err := RetryableOperation(ctx, func() error {
			_, _, err := client.GetInner().Issues.CreateComment(ctx, owner, repo, prNumber, 
				&githublib.IssueComment{Body: &comment})
			return err
		})
		
		if err != nil {
			logger.Warn("Failed to add merged comment for PR", "number", prNumber, "error", err)
		}
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