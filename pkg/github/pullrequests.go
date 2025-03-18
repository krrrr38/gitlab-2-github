package github

import (
	"context"
	"fmt"

	githublib "github.com/google/go-github/v60/github"
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
	"github.com/krrrr38/gitlab-2-github/pkg/utils"
	"github.com/shurcooL/githubv4"
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

// CreatePRReview creates a single review comment and returns the review ID
func CreatePRReview(ctx context.Context, client *Client, owner, repo string, prNumber int, body, path string, position int, resolved bool) (*githublib.PullRequestReview, error) {
	logger.Debug("Creating PR review comment",
		"owner", owner,
		"repo", repo,
		"prNumber", prNumber,
		"path", path,
		"position", position,
		"resolved", resolved)

	// 文字数制限に合わせて切り詰める
	truncatedBody := utils.TruncateText(body, utils.MaxCommentLength)

	// First get the latest commit SHA for the PR
	var commitSHA string
	err := RetryableOperation(ctx, func() error {
		pr, _, err := client.GetInner().PullRequests.Get(ctx, owner, repo, prNumber)
		if err != nil {
			return err
		}
		if pr.Head != nil && pr.Head.SHA != nil {
			commitSHA = *pr.Head.SHA
		}
		return nil
	})

	if err != nil {
		logger.Error("Failed to get PR details for comment",
			"owner", owner,
			"repo", repo,
			"prNumber", prNumber,
			"error", err)
		return nil, fmt.Errorf("failed to get PR details for comment: %w", err)
	}

	if commitSHA == "" {
		return nil, fmt.Errorf("could not determine HEAD commit SHA for PR")
	}

	// Get diff information so we can create a proper review
	var fileFound bool
	err = RetryableOperation(ctx, func() error {
		// Get the full PR diff
		opts := &githublib.ListOptions{
			PerPage: 100,
		}
		files, _, err := client.GetInner().PullRequests.ListFiles(ctx, owner, repo, prNumber, opts)
		if err != nil {
			return err
		}

		// Find the file that matches our path
		for _, file := range files {
			if file.GetFilename() == path {
				fileFound = true
				break
			}
		}
		return nil
	})

	if err != nil {
		logger.Error("Failed to get PR diff", "error", err)
	}

	// If we couldn't find the file in the diff, create a regular comment instead
	if !fileFound {
		logger.Warn("Could not find file in PR diff, creating regular comment instead", "path", path)
		err := CreatePRComment(ctx, client, owner, repo, prNumber, truncatedBody)
		if err != nil {
			return nil, err
		}
		// Return an empty review since we created a regular comment
		return &githublib.PullRequestReview{}, nil
	}

	// Create a draft review with the comment
	var review *githublib.PullRequestReview
	err = RetryableOperation(ctx, func() error {
		// Create a draft review comment that uses line numbers instead of position
		// This is more reliable for GitHub API
		draftComment := &githublib.DraftReviewComment{
			Path: githublib.String(path),
			Body: githublib.String(truncatedBody),
		}

		// Prefer line property over position if available
		if position > 0 {
			draftComment.Line = githublib.Int(position)
		} else {
			// Fallback to position if needed
			draftComment.Position = githublib.Int(1) // Default to first line if position is invalid
		}

		// Create the review request with the comment
		reviewRequest := &githublib.PullRequestReviewRequest{
			CommitID: githublib.String(commitSHA),
			Body:     githublib.String(""),
			Event:    githublib.String("COMMENT"),
			Comments: []*githublib.DraftReviewComment{draftComment},
		}

		var err error
		review, _, err = client.GetInner().PullRequests.CreateReview(ctx, owner, repo, prNumber, reviewRequest)
		return err
	})

	if err != nil {
		logger.Error("Failed to create review", "error", err)
		return nil, err
	}

	// If the comment should be resolved, use GraphQL API to resolve it
	if resolved && review != nil && review.ID != nil {
		// Get the review thread ID using the review ID
		// This requires an additional GraphQL query
		err = resolveReviewThread(ctx, client, owner, repo, prNumber, *review.ID)
		if err != nil {
			logger.Warn("Failed to resolve review thread",
				"reviewID", review.GetID(),
				"error", err)
			// We'll continue even if resolving failed
		} else {
			logger.Info("Successfully resolved review thread",
				"reviewID", review.GetID())
		}
	}

	return review, nil
}

// CreatePRReviewComment is a legacy method that creates a review comment
// This function now uses CreatePRReview internally
func CreatePRReviewComment(ctx context.Context, client *Client, owner, repo string, prNumber int, body, path string, position int) (*githublib.PullRequestComment, error) {
	review, err := CreatePRReview(ctx, client, owner, repo, prNumber, body, path, position, false)
	if err != nil {
		return nil, err
	}

	// For backward compatibility, return a comment structure
	// (this is not ideal but maintains compatibility)
	comment := &githublib.PullRequestComment{
		Body: githublib.String(body),
		Path: githublib.String(path),
		ID:   review.ID, // Use review ID as comment ID
	}

	return comment, nil
}

// resolveReviewThread resolves a review thread using GitHub's GraphQL API
func resolveReviewThread(ctx context.Context, client *Client, owner, repo string, prNumber int, reviewID int64) error {
	// First, we need to get the thread ID from the review ID
	// The review ID doesn't directly map to the thread ID in GitHub's GraphQL API

	// Define the query
	var query struct {
		Repository struct {
			PullRequest struct {
				ReviewThreads struct {
					Nodes []struct {
						ID         string
						IsResolved bool
						Comments   struct {
							Nodes []struct {
								ID                string
								PullRequestReview struct {
									DatabaseID int64
								}
							}
						} `graphql:"comments(first: 5)"`
					}
				} `graphql:"reviewThreads(first: 50)"`
			} `graphql:"pullRequest(number: $prNumber)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	// Set up query variables
	variables := map[string]interface{}{
		"owner":    githubv4.String(owner),
		"name":     githubv4.String(repo),
		"prNumber": githubv4.Int(prNumber),
	}

	// Execute the query to get review threads
	err := client.GetV4().Query(ctx, &query, variables)
	if err != nil {
		return fmt.Errorf("failed to query review threads: %w", err)
	}

	// Find the thread that belongs to our review
	var threadID string
	for _, thread := range query.Repository.PullRequest.ReviewThreads.Nodes {
		// Skip already resolved threads
		if thread.IsResolved {
			continue
		}

		// Check if any comment in this thread belongs to our review
		for _, comment := range thread.Comments.Nodes {
			if comment.PullRequestReview.DatabaseID == reviewID {
				threadID = thread.ID
				break
			}
		}

		if threadID != "" {
			break
		}
	}

	if threadID == "" {
		return fmt.Errorf("could not find thread for review ID %d", reviewID)
	}

	// Now that we have the thread ID, we can resolve it
	var mutation struct {
		ResolveReviewThread struct {
			Thread struct {
				ID string
			}
		} `graphql:"resolveReviewThread(input: $input)"`
	}

	// Set up the mutation input
	resolveInput := githubv4.ResolveReviewThreadInput{
		ThreadID: githubv4.ID(threadID),
	}

	// Execute the mutation to resolve the thread
	err = client.GetV4().Mutate(ctx, &mutation, resolveInput, nil)

	if err != nil {
		return fmt.Errorf("failed to resolve review thread: %w", err)
	}

	return nil
}

// CreatePRReviewCommentReply creates a reply to an existing review comment
func CreatePRReviewCommentReply(ctx context.Context, client *Client, owner, repo string, prNumber int, body string, inReplyTo int64, resolved bool) (*githublib.PullRequestComment, error) {
	logger.Debug("Creating PR review comment reply",
		"owner", owner,
		"repo", repo,
		"prNumber", prNumber,
		"inReplyTo", inReplyTo,
		"resolved", resolved)

	// 文字数制限に合わせて切り詰める
	truncatedBody := utils.TruncateText(body, utils.MaxCommentLength)

	// Verify the original comment exists
	err := RetryableOperation(ctx, func() error {
		_, _, err := client.GetInner().PullRequests.GetComment(ctx, owner, repo, inReplyTo)
		return err
	})

	if err != nil {
		logger.Error("Failed to get original comment for reply",
			"owner", owner,
			"repo", repo,
			"commentID", inReplyTo,
			"error", err)
		return nil, fmt.Errorf("failed to get original comment for reply: %w", err)
	}

	// Get the latest commit SHA for the PR
	var commitSHA string
	err = RetryableOperation(ctx, func() error {
		pr, _, err := client.GetInner().PullRequests.Get(ctx, owner, repo, prNumber)
		if err != nil {
			return err
		}
		if pr.Head != nil && pr.Head.SHA != nil {
			commitSHA = *pr.Head.SHA
		}
		return nil
	})

	if err != nil {
		logger.Error("Failed to get PR details for comment reply",
			"owner", owner,
			"repo", repo,
			"prNumber", prNumber,
			"error", err)
		return nil, fmt.Errorf("failed to get PR details for comment reply: %w", err)
	}

	if commitSHA == "" {
		return nil, fmt.Errorf("could not determine HEAD commit SHA for PR")
	}

	// Create a reply using the standard GitHub API
	var comment *githublib.PullRequestComment
	err = RetryableOperation(ctx, func() error {
		reviewComment := &githublib.PullRequestComment{
			Body:      &truncatedBody,
			InReplyTo: githublib.Int64(inReplyTo),
			CommitID:  &commitSHA,
		}

		var createErr error
		comment, _, createErr = client.GetInner().PullRequests.CreateComment(ctx, owner, repo, prNumber, reviewComment)
		return createErr
	})

	if err != nil {
		return nil, err
	}

	// If resolved flag is set, try to resolve this comment thread
	if resolved && comment != nil && comment.ID != nil {
		threadErr := resolveReviewThread(ctx, client, owner, repo, prNumber, *comment.ID)
		if threadErr != nil {
			logger.Warn("Failed to resolve review thread for reply",
				"commentID", comment.GetID(),
				"error", threadErr)
			// Continue even if resolving failed
		} else {
			logger.Info("Successfully resolved review thread for reply",
				"commentID", comment.GetID())
		}
	}

	return comment, nil
}
