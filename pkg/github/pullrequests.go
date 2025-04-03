package github

import (
	"context"
	"fmt"
	"time"

	githublib "github.com/google/go-github/v70/github"
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

func (client *Client) GetClosedPullRequestTitles(ctx context.Context, owner, repo string) ([]string, error) {
	var titles []string
	var page = 1
	for {
		opts := &githublib.PullRequestListOptions{
			State: "closed",
			ListOptions: githublib.ListOptions{
				PerPage: 100,
				Page:    page,
			},
		}
		prs, _, err := client.GetInner().PullRequests.List(ctx, owner, repo, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to get GitHub PRs: %w", err)
		}
		for _, pr := range prs {
			titles = append(titles, pr.GetTitle())
		}
		if len(prs) < 100 {
			break
		}
		page += 1
	}
	return titles, nil
}

func (client *Client) GetOpenedPullRequests(ctx context.Context, owner, repo string) ([]*githublib.PullRequest, error) {
	var ret []*githublib.PullRequest
	var page = 1
	for {
		opts := &githublib.PullRequestListOptions{
			State: "opened",
			ListOptions: githublib.ListOptions{
				PerPage: 100,
				Page:    page,
			},
		}
		prs, _, err := client.GetInner().PullRequests.List(ctx, owner, repo, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to get GitHub PRs: %w", err)
		}
		for _, pr := range prs {
			ret = append(ret, pr)
		}
		if len(prs) < 100 {
			break
		}
		page += 1
	}
	return ret, nil
}

// CreatePullRequest creates a new pull request in GitHub
func (client *Client) CreatePullRequest(ctx context.Context, owner, repo string, opts *PullRequestOptions) (*githublib.PullRequest, error) {
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

	return pr, nil
}

func (client *Client) AddLabelsToIssue(ctx context.Context, owner, repo string, issueNumber int, labels []string) error {
	// Log the operation with key parameters
	logger.Debug("Adding labels to issue",
		"owner", owner,
		"repo", repo,
		"issueNumber", issueNumber,
		"labels", labels)

	// Add labels to the issue
	err := RetryableOperation(ctx, func() error {
		_, _, err := client.GetInner().Issues.AddLabelsToIssue(ctx, owner, repo, issueNumber, labels)
		return err
	})

	if err != nil {
		logger.Error("Failed to add labels to issue",
			"owner", owner,
			"repo", repo,
			"issueNumber", issueNumber,
			"labels", labels,
			"error", err)
		return fmt.Errorf("failed to add labels to issue: %w", err)
	}

	return nil
}

// UpdatePullRequestTitle edit a pull request title
func (client *Client) UpdatePullRequestTitle(ctx context.Context, owner, repo string, prNumber int, title string) error {
	// Log the operation with key parameters
	logger.Debug("Updating pull request",
		"owner", owner,
		"repo", repo,
		"prNumber", prNumber)

	// Edit the PR with retries
	err := RetryableOperation(ctx, func() error {
		updateRequest := &githublib.PullRequest{
			Title: githublib.String(title),
		}
		_, resp, err := client.GetInner().PullRequests.Edit(ctx, owner, repo, prNumber, updateRequest)
		xGitHubRequestId := resp.Header.Get("x-github-request-id")
		if err != nil {
			err = fmt.Errorf("%w, x-github-request-id: %s", err, xGitHubRequestId)
		}
		return err
	})

	if err != nil {
		logger.Error("Failed to update GitHub PR",
			"owner", owner,
			"repo", repo,
			"prNumber", prNumber,
			"error", err)
		return fmt.Errorf("failed to update GitHub PR: %w", err)
	}

	return nil
}

// ClosePullRequest closes a pull request
func (client *Client) ClosePullRequest(ctx context.Context, owner, repo string, prNumber int) error {
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
		_, resp, err := client.GetInner().PullRequests.Edit(ctx, owner, repo, prNumber, closeRequest)
		xGitHubRequestId := resp.Header.Get("x-github-request-id")
		if err != nil {
			err = fmt.Errorf("%w, x-github-request-id: %s", err, xGitHubRequestId)
		}
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
func (client *Client) DeleteBranch(ctx context.Context, owner, repo, branch string) error {
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

	logger.Debug("Deleted branch", "branch", branch)
	return nil
}

// CreateIssueComment creates a regular (non-review) comment on a pull request
func (client *Client) CreateIssueComment(ctx context.Context, owner, repo string, prNumber int, body string, resolved bool) (*githublib.IssueComment, error) {
	// 文字数制限に合わせて切り詰める
	truncatedBody := utils.TruncateText(body, utils.MaxCommentLength)
	if resolved {
		// resolveされている場合は折りたたむ (github apiでresolvedとするにはgraphql apiを利用する必要があり、手間がかかるため短期解を選択)
		truncatedBody = utils.WrapCommentAsResolved(truncatedBody)
	}

	var comment *githublib.IssueComment
	err := RetryableOperation(ctx, func() error {
		// https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api?apiVersion=2022-11-28#calculating-points-for-the-secondary-rate-limit
		time.Sleep(1 * time.Second) // In general, no more than 80 content-generating requests per minute
		c, resp, err := client.GetInner().Issues.CreateComment(ctx, owner, repo, prNumber,
			&githublib.IssueComment{Body: &truncatedBody})
		comment = c
		xGitHubRequestId := resp.Header.Get("x-github-request-id")
		if err != nil {
			err = fmt.Errorf("%w, x-github-request-id: %s", err, xGitHubRequestId)
		}
		return err
	})
	return comment, err
}

// CreateCommitComment creates a regular (non-review) comment on a pull request
func (client *Client) CreateCommitComment(ctx context.Context, owner, repo, commit string, body string) error {
	// 文字数制限に合わせて切り詰める
	truncatedBody := utils.TruncateText(body, utils.MaxCommentLength)
	err := RetryableOperation(ctx, func() error {
		// https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api?apiVersion=2022-11-28#calculating-points-for-the-secondary-rate-limit
		time.Sleep(1 * time.Second) // In general, no more than 80 content-generating requests per minute
		comment := &struct {
			Body string `json:"body,omitempty"`
		}{
			Body: truncatedBody,
		}
		u := fmt.Sprintf("repos/%v/%v/commits/%s/comments", owner, repo, commit)
		req, err := client.GetInner().NewRequest("POST", u, comment)
		if err != nil {
			return err
		}
		c := new(githublib.PullRequestComment)
		var resp *githublib.Response
		resp, err = client.GetInner().Do(ctx, req, c)
		xGitHubRequestId := resp.Header.Get("x-github-request-id")
		if err != nil {
			err = fmt.Errorf("%w, x-github-request-id: %s", err, xGitHubRequestId)
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to create commit comment %w", err)
	}
	return nil
}

type CreatePRCommentInput struct {
	Owner     string
	Repo      string
	PrNumber  int
	Body      string
	Path      string
	Sha1      string
	Resolved  bool
	StartLine *int
	LastLine  *int
}

// CreatePRComment creates a single review comment and returns the review ID
func (client *Client) CreatePRComment(ctx context.Context, input *CreatePRCommentInput) (*githublib.PullRequestComment, error) {
	logger.Debug("Creating PR comment",
		"owner", input.Owner,
		"repo", input.Repo,
		"prNumber", input.PrNumber,
		"path", input.Path,
		"startLine", input.StartLine,
		"lastLine", input.LastLine,
		"resolved", input.Resolved)

	// 文字数制限に合わせて切り詰める
	truncatedBody := utils.TruncateText(input.Body, utils.MaxCommentLength)
	if input.Resolved {
		// resolveされている場合は折りたたむ (github apiでresolvedとするにはgraphql apiを利用する必要があり、手間がかかるため短期解を選択)
		truncatedBody = utils.WrapCommentAsResolved(truncatedBody)
	}

	// Create a draft review with the comment
	var comment *githublib.PullRequestComment
	err := RetryableOperation(ctx, func() error {
		// https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api?apiVersion=2022-11-28#calculating-points-for-the-secondary-rate-limit
		time.Sleep(1 * time.Second) // In general, no more than 80 content-generating requests per minute
		var startLine *int
		if input.StartLine != nil && input.LastLine != nil && *input.StartLine < *input.LastLine {
			startLine = input.StartLine
		}
		prComment := &githublib.PullRequestComment{
			// required
			Body:     githublib.String(truncatedBody),
			CommitID: githublib.String(input.Sha1),
			Path:     githublib.String(input.Path),
			// optional
			StartLine: startLine,
			Line:      input.LastLine, // For a multi-line comment, the last line of the range that your comment applies to.
		}

		var err error
		var resp *githublib.Response
		comment, resp, err = client.GetInner().PullRequests.CreateComment(ctx, input.Owner, input.Repo, input.PrNumber, prComment)
		xGitHubRequestId := resp.Header.Get("x-github-request-id")
		if err != nil {
			err = fmt.Errorf("%w, x-github-request-id: %s", err, xGitHubRequestId)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return comment, nil
}

type CreatePRCommentReplyInput struct {
	Owner     string
	Repo      string
	PrNumber  int
	Body      string
	CommentID int64
	Resolved  bool
}

// CreatePRCommentReply creates a reply to an existing review comment
func (client *Client) CreatePRCommentReply(ctx context.Context, input *CreatePRCommentReplyInput) error {
	logger.Debug("Creating PR review comment reply",
		"owner", input.Owner,
		"repo", input.Repo,
		"prNumber", input.PrNumber,
		"commentID", input.CommentID,
		"resolved", input.Resolved)

	// 文字数制限に合わせて切り詰める
	truncatedBody := utils.TruncateText(input.Body, utils.MaxCommentLength)
	if input.Resolved {
		// resolveされている場合は折りたたむ (github apiでresolvedとするにはgraphql apiを利用する必要があり、手間がかかるため短期解を選択)
		truncatedBody = utils.WrapCommentAsResolved(truncatedBody)
	}

	err := RetryableOperation(ctx, func() error {
		// https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api?apiVersion=2022-11-28#calculating-points-for-the-secondary-rate-limit
		time.Sleep(1 * time.Second) // In general, no more than 80 content-generating requests per minute
		comment := &struct {
			Body string `json:"body,omitempty"`
		}{
			Body: truncatedBody,
		}
		u := fmt.Sprintf("repos/%v/%v/pulls/%d/comments/%d/replies", input.Owner, input.Repo, input.PrNumber, input.CommentID)
		req, err := client.GetInner().NewRequest("POST", u, comment)
		if err != nil {
			return err
		}
		c := new(githublib.PullRequestComment)
		var resp *githublib.Response
		resp, err = client.GetInner().Do(ctx, req, c)
		xGitHubRequestId := resp.Header.Get("x-github-request-id")
		if err != nil {
			err = fmt.Errorf("%w, x-github-request-id: %s", err, xGitHubRequestId)
		}
		return err
	})
	if err != nil {
		logger.Error("Failed to create comment reply", "error", err)
		return err
	}
	return nil
}
