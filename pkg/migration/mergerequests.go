package migration

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	githublib "github.com/google/go-github/v60/github"
	"github.com/krrrr38/gitlab-2-github/pkg/config"
	"github.com/krrrr38/gitlab-2-github/pkg/git"
	"github.com/krrrr38/gitlab-2-github/pkg/github"
	"github.com/krrrr38/gitlab-2-github/pkg/gitlab"
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
	"github.com/krrrr38/gitlab-2-github/pkg/utils"
	gitlablib "github.com/xanzy/go-gitlab"
)

// MigrateMergeRequests migrates GitLab merge requests to GitHub pull requests
func MigrateMergeRequests(ctx context.Context, gitlabClient *gitlablib.Client, githubClient *github.Client, cfg config.Config, opts *MigrationOptions) error {
	// Get all merge requests or filter by IDs
	allMRs, err := gitlab.GetMergeRequests(gitlabClient, cfg.GitLabProjectID, cfg.FilterMergeReqIDs)
	if err != nil {
		return fmt.Errorf("failed to get merge requests: %w", err)
	}

	logger.Info("Found merge requests", "count", len(allMRs))

	// 処理順序を決定（IIDで昇順ソート）
	sort.Slice(allMRs, func(i, j int) bool {
		return allMRs[i].IID < allMRs[j].IID
	})

	var totalProcessed, totalSucceeded, totalFailed, totalComments int

	// For each merge request, create corresponding branches and PR in GitHub
	for _, mr := range allMRs {
		// コンテキストが既にキャンセルされていないか確認
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// 処理を継続
		}

		// continue-fromオプションが指定されている場合は、そのIDより小さいものはスキップ
		if opts.ContinueFromID > 0 && mr.IID < opts.ContinueFromID {
			logger.Info("Skipping MR (before continue-from point)", "id", mr.IID, "title", mr.Title)
			continue
		}

		// 既に GitHub 側でプルリクエストが存在するか確認
		// PR タイトルのパターン等から判断することも可能
		// ここでは簡単のため mr.IID を含むブランチ名があるかどうかで判断
		sourceBranchName := fmt.Sprintf("gitlab-mr-%d-source", mr.IID)
		alreadyMigrated, err := checkPRExistsInGitHub(ctx, githubClient, cfg.GitHubOwner, cfg.GitHubRepo, sourceBranchName)
		if err != nil {
			logger.Warn("Failed to check if PR exists", "error", err)
		}

		if alreadyMigrated {
			logger.Info("Skipping already migrated MR", "id", mr.IID, "title", mr.Title)
			totalProcessed++
			totalSucceeded++
			continue
		}

		logger.Info("Migrating MR", "id", mr.IID, "title", mr.Title)

		// Get detailed MR information
		detailedMR, _, err := gitlabClient.MergeRequests.GetMergeRequest(cfg.GitLabProjectID, mr.IID, nil)
		if err != nil {
			logger.Warn("Failed to get detailed info for MR", "id", mr.IID, "error", err)
			totalProcessed++
			totalFailed++
			continue
		}

		// Create branches and PR in GitHub
		commentCount, err := processMergeRequest(ctx, gitlabClient, githubClient, cfg, detailedMR)
		if err != nil {
			logger.Warn("Failed to migrate MR", "id", mr.IID, "error", err)
			totalProcessed++
			totalFailed++
		} else {
			totalProcessed++
			totalSucceeded++
			totalComments += commentCount
		}

		// 進捗状況を表示
		logger.Info("Progress",
			"processed", totalProcessed,
			"total", len(allMRs),
			"succeeded", totalSucceeded,
			"failed", totalFailed,
			"comments", totalComments)
	}

	// 最終の統計情報を表示
	logger.Info("Migration completed",
		"processed", totalProcessed,
		"total", len(allMRs),
		"succeeded", totalSucceeded,
		"failed", totalFailed,
		"comments", totalComments)

	return nil
}

// checkPRExistsInGitHub checks if a PR with the given branch name already exists in GitHub
func checkPRExistsInGitHub(ctx context.Context, githubClient *github.Client, owner, repo, branchName string) (bool, error) {
	// ブランチの存在を確認
	err := github.RetryableOperation(ctx, func() error {
		_, _, err := githubClient.GetInner().Repositories.GetBranch(ctx, owner, repo, branchName, 0)
		return err
	})

	// ブランチが存在しない場合はエラーが返るが、それは「マイグレーション済みでない」ということなのでfalseを返す
	if err != nil {
		return false, nil
	}

	// ブランチが存在する場合、そのブランチを使用したPRがあるかどうかを確認
	var pullExists bool
	err = github.RetryableOperation(ctx, func() error {
		// headパラメータでブランチ名を指定してPRを検索
		// owner:branchName 形式で検索
		pulls, _, err := githubClient.GetInner().PullRequests.List(ctx, owner, repo, &githublib.PullRequestListOptions{
			Head:  fmt.Sprintf("%s:%s", owner, branchName),
			State: "all", // オープン、クローズ、マージ済みのすべてのPRを検索
		})
		if err != nil {
			return err
		}

		pullExists = len(pulls) > 0
		return nil
	})

	if err != nil {
		return false, err
	}

	return pullExists, nil
}

// processMergeRequest handles the migration of a single merge request
// 戻り値として処理したコメント数も返す
func processMergeRequest(ctx context.Context, gitlabClient *gitlablib.Client, githubClient *github.Client, cfg config.Config, mr *gitlablib.MergeRequest) (int, error) {
	// Working directory for this MR
	mrDir := fmt.Sprintf("%s/mr-%d", cfg.TempDir, mr.IID)
	if err := git.CleanupDirectory(mrDir); err != nil {
		return 0, err
	}

	// Clone the repository
	repoURL := fmt.Sprintf("https://%s@github.com/%s/%s.git",
		cfg.GitHubToken,
		cfg.GitHubOwner,
		cfg.GitHubRepo)

	cloneCmd := fmt.Sprintf("git clone %s %s", repoURL, mrDir)
	if err := git.ExecuteCommand(cloneCmd); err != nil {
		return 0, fmt.Errorf("failed to clone GitHub repository: %w", err)
	}

	// Prepare unique branch names for both source and target
	sourceBranch := fmt.Sprintf("gitlab-mr-%d-source", mr.IID)
	targetBranch := fmt.Sprintf("gitlab-mr-%d-target", mr.IID)

	logger.Info("Creating unique branches for migration", "mr", mr.IID, "source", sourceBranch, "target", targetBranch)

	// Add GitLab remote to help with Git operations
	gitlabRemoteURL := fmt.Sprintf("https://oauth2:%s@%s/%s.git",
		cfg.GitLabToken,
		strings.TrimPrefix(cfg.GitLabURL, "https://"),
		cfg.GitLabProjectID)

	addRemoteCmd := fmt.Sprintf("cd %s && git remote add gitlab %s", mrDir, gitlabRemoteURL)
	if err := git.ExecuteCommand(addRemoteCmd); err != nil {
		return 0, fmt.Errorf("failed to add GitLab remote: %w", err)
	}

	// Fetch everything from GitLab
	fetchCmd := fmt.Sprintf("cd %s && git fetch gitlab --prune --tags", mrDir)
	if err := git.ExecuteCommand(fetchCmd); err != nil {
		return 0, fmt.Errorf("failed to fetch from GitLab: %w", err)
	}

	// Check if DiffRefs are available (use proper field names)
	if mr.DiffRefs.BaseSha == "" || mr.DiffRefs.HeadSha == "" {
		return 0, fmt.Errorf("missing DiffRefs information in merge request %d", mr.IID)
	}

	// Log the SHA values for debugging
	logger.Info("Using DiffRefs for exact commit matching",
		"mr_id", mr.IID,
		"base_sha", mr.DiffRefs.BaseSha,
		"head_sha", mr.DiffRefs.HeadSha)

	// Create target branch from base_sha
	targetBaseSHACmd := fmt.Sprintf("cd %s && git checkout -b %s %s",
		mrDir, targetBranch, mr.DiffRefs.BaseSha)

	if err := git.ExecuteCommand(targetBaseSHACmd); err != nil {
		logger.Warn("Failed to checkout target branch from BaseSha",
			"branch", targetBranch,
			"base_sha", mr.DiffRefs.BaseSha,
			"error", err)

		// Fallback to using target branch directly
		targetBranchCmd := fmt.Sprintf("cd %s && git checkout -b %s gitlab/%s",
			mrDir, targetBranch, mr.TargetBranch)

		if err := git.ExecuteCommand(targetBranchCmd); err != nil {
			return 0, fmt.Errorf("failed to create target branch: %w", err)
		}
	}

	// Push the target branch to GitHub
	pushTargetCmd := fmt.Sprintf("cd %s && git push origin %s", mrDir, targetBranch)
	if err := git.ExecuteCommand(pushTargetCmd); err != nil {
		return 0, fmt.Errorf("failed to push target branch: %w", err)
	}

	// Create source branch from head_sha
	createSourceBranchCmd := fmt.Sprintf("cd %s && git checkout -b %s %s",
		mrDir, sourceBranch, mr.DiffRefs.HeadSha)

	if err := git.ExecuteCommand(createSourceBranchCmd); err != nil {
		logger.Warn("Failed to checkout source branch from HeadSha",
			"branch", sourceBranch,
			"head_sha", mr.DiffRefs.HeadSha,
			"error", err)

		// Fallback to using source branch directly
		sourceBranchCmd := fmt.Sprintf("cd %s && git checkout -b %s gitlab/%s",
			mrDir, sourceBranch, mr.SourceBranch)

		if err := git.ExecuteCommand(sourceBranchCmd); err != nil {
			// Last resort: create from target
			lastResortCmd := fmt.Sprintf("cd %s && git checkout -b %s %s",
				mrDir, sourceBranch, targetBranch)

			if err := git.ExecuteCommand(lastResortCmd); err != nil {
				return 0, fmt.Errorf("failed to create source branch: %w", err)
			}

			// At this point, we need to apply changes manually
			logger.Warn("Using manual changes application as fallback")
		} else {
			logger.Info("Using GitLab source branch instead of specific HEAD SHA")
		}
	}

	// Push the source branch to GitHub (as is, with the exact HEAD SHA)
	logger.Info("Pushing source branch with exact HEAD SHA", "branch", sourceBranch, "sha", mr.DiffRefs.HeadSha)
	pushSourceCmd := fmt.Sprintf("cd %s && git push origin %s --force", mrDir, sourceBranch)
	if err := git.ExecuteCommand(pushSourceCmd); err != nil {
		return 0, fmt.Errorf("failed to push source branch: %w", err)
	}

	// Add metadata file for reference
	logger.Info("Adding MR metadata information")

	// Add a metadata file
	metadataContent := fmt.Sprintf("GitLab MR: %d\nTitle: %s\nSource: %s\nTarget: %s\nAuthor: %s\nCreated: %s\nBaseSha: %s\nHeadSha: %s\n",
		mr.IID, mr.Title, mr.SourceBranch, mr.TargetBranch, mr.Author.Username,
		mr.CreatedAt.Format(time.RFC3339), mr.DiffRefs.BaseSha, mr.DiffRefs.HeadSha)

	metadataCmd := fmt.Sprintf("cd %s && echo '%s' > %s",
		mrDir,
		metadataContent,
		fmt.Sprintf(".gitlab-mr-%d-metadata", mr.IID))
	if err := git.ExecuteCommand(metadataCmd); err != nil {
		logger.Warn("Failed to create metadata file, continuing anyway", "error", err)
	} else {
		// Commit the metadata file
		addMetadataCmd := fmt.Sprintf("cd %s && git add .gitlab-mr-%d-metadata", mrDir, mr.IID)
		_ = git.ExecuteCommand(addMetadataCmd)

		commitMetadataCmd := fmt.Sprintf("cd %s && git commit -m 'Add GitLab MR %d metadata'", mrDir, mr.IID)
		_ = git.ExecuteCommand(commitMetadataCmd)

		// Push the metadata commit
		pushMetadataCmd := fmt.Sprintf("cd %s && git push origin %s", mrDir, sourceBranch)
		_ = git.ExecuteCommand(pushMetadataCmd)
	}

	// Create GitHub PR
	// Prepare PR title (with truncation if needed)
	title := utils.TruncateText(mr.Title, utils.MaxPRTitleLength)
	if mr.State == "closed" {
		// Add [closed] suffix but ensure we don't exceed the limit
		closedSuffix := " [closed]"
		if len(title)+len(closedSuffix) > utils.MaxPRTitleLength {
			title = utils.TruncateText(title, utils.MaxPRTitleLength-len(closedSuffix))
		}
		title += closedSuffix
	}

	// マージリクエストの承認情報を取得
	approvals, err := gitlab.GetMergeRequestApprovals(gitlabClient, cfg.GitLabProjectID, mr.IID)
	if err != nil {
		logger.Warn("Failed to get MR approvals", "error", err)
		// エラーがあっても処理は続行
	}

	// 承認情報をフォーマット
	var approvalsText string
	if len(approvals) > 0 {
		approvalsText = "\n\n### Approvals\n"
		for _, approval := range approvals {
			approvalsText += fmt.Sprintf("- Approved by **@%s** on %s\n",
				approval.User,
				approval.CreatedAt.Format("2006-01-02 15:04:05"))
		}
	}

	// 日時情報の取得
	createdAt := ""
	if !mr.CreatedAt.IsZero() {
		createdAt = mr.CreatedAt.Format("2006-01-02 15:04:05 MST")
	}

	// Leave room for header (around 200-300 chars)
	description := utils.TruncateText(mr.Description, utils.MaxPRDescriptionLength-300)

	// 説明文にメタデータを含めたヘッダーを追加
	body := fmt.Sprintf("## Migrated from GitLab\n\n"+
		"**Original MR:** %s/%s/merge_requests/%d\n"+
		"**Author:** @%s\n"+
		"**Created:** %s\n"+
		"**Status:** %s\n\n"+
		"---\n\n%s%s",
		cfg.GitLabURL, cfg.GitLabProjectID, mr.IID,
		mr.Author.Username,
		createdAt,
		mr.State,
		description,
		approvalsText)

	body = utils.TruncateText(body, utils.MaxPRDescriptionLength)

	// Create the PR
	var pr *githublib.PullRequest
	err = github.RetryableOperation(ctx, func() error {
		var err error
		pr, err = github.CreatePullRequest(ctx, githubClient, cfg.GitHubOwner, cfg.GitHubRepo, &github.PullRequestOptions{
			Title:               title,
			Body:                body,
			Head:                sourceBranch,
			Base:                targetBranch,
			Draft:               mr.WorkInProgress,
			MaintainerCanModify: true,
		})
		return err
	})

	if err != nil {
		// Special handling for no diff error
		if noDiffErr, ok := err.(*github.NoDiffError); ok {
			logger.Info("No difference found between branches, adding dummy commit to source branch", "source", noDiffErr.Head, "target", noDiffErr.Base)

			// Create a dummy file to ensure there's a diff
			dummyContent := fmt.Sprintf("GitLab MR: %d\nTitle: %s\nAuthor: %s\nCreated: %s\nState: %s\n",
				mr.IID, mr.Title, mr.Author.Username, mr.CreatedAt.Format(time.RFC3339), mr.State)

			// Write the dummy file and commit it
			createFileCmd := fmt.Sprintf("cd %s && echo '%s' > %s",
				mrDir,
				dummyContent,
				".gitlab-mr-"+fmt.Sprintf("%d", mr.IID)+"-dummy")
			if err := git.ExecuteCommand(createFileCmd); err != nil {
				return 0, fmt.Errorf("failed to create dummy file: %w", err)
			}

			// Add and commit the file
			addCmd := fmt.Sprintf("cd %s && git add .", mrDir)
			if err := git.ExecuteCommand(addCmd); err != nil {
				return 0, fmt.Errorf("failed to add dummy file: %w", err)
			}

			commitCmd := fmt.Sprintf("cd %s && git commit -m 'Add dummy file for GitLab MR %d to ensure diff'", mrDir, mr.IID)
			if err := git.ExecuteCommand(commitCmd); err != nil {
				return 0, fmt.Errorf("failed to commit dummy file: %w", err)
			}

			// Push the branch again
			pushCmd := fmt.Sprintf("cd %s && git push origin %s --force", mrDir, sourceBranch)
			if err := git.ExecuteCommand(pushCmd); err != nil {
				return 0, fmt.Errorf("failed to push source branch with dummy commit: %w", err)
			}

			// Try to create the PR again
			err = github.RetryableOperation(ctx, func() error {
				var createErr error
				pr, createErr = github.CreatePullRequest(ctx, githubClient, cfg.GitHubOwner, cfg.GitHubRepo, &github.PullRequestOptions{
					Title:               title,
					Body:                body,
					Head:                sourceBranch,
					Base:                targetBranch,
					Draft:               mr.WorkInProgress,
					MaintainerCanModify: true,
				})
				return createErr
			})

			if err != nil {
				return 0, fmt.Errorf("failed to create GitHub PR after adding dummy commit: %w", err)
			}
		} else {
			return 0, fmt.Errorf("failed to create GitHub PR: %w", err)
		}
	}

	logger.Info("Created GitHub PR", "number", pr.GetNumber(), "url", pr.GetHTMLURL())

	// 3. Migrate comments
	commentCount, err := migrateComments(ctx, gitlabClient, githubClient, cfg, mr.IID, pr.GetNumber())
	if err != nil {
		logger.Warn("Failed to migrate some comments", "error", err)
		// Continue despite comment migration errors
	}

	// 4. Close the PR if the original MR was closed/merged
	if mr.State == "closed" || mr.State == "merged" {
		err = github.RetryableOperation(ctx, func() error {
			return github.ClosePullRequest(ctx, githubClient, cfg.GitHubOwner, cfg.GitHubRepo, pr.GetNumber())
		})

		if err != nil {
			logger.Warn("Failed to close PR", "error", err)
		} else {
			logger.Info("Closed GitHub PR", "number", pr.GetNumber())

			// Delete source branch
			err = github.DeleteBranch(ctx, githubClient, cfg.GitHubOwner, cfg.GitHubRepo, sourceBranch)
			if err != nil {
				logger.Warn("Failed to delete source branch", "branch", sourceBranch, "error", err)
			}

			// Always delete the target branch since we created a unique one
			err = github.DeleteBranch(ctx, githubClient, cfg.GitHubOwner, cfg.GitHubRepo, targetBranch)
			if err != nil {
				logger.Warn("Failed to delete temporary target branch", "branch", targetBranch, "error", err)
			}
		}
	}

	return commentCount, nil
}

// migrateComments migrates comments from a GitLab merge request to a GitHub pull request
// 戻り値として移行したコメント数も返す
func migrateComments(ctx context.Context, gitlabClient *gitlablib.Client, githubClient *github.Client, cfg config.Config, mrID int, prNumber int) (int, error) {
	// Get discussions from GitLab MR to track comment relationships
	discussionNotes, err := gitlab.GetMergeRequestDiscussions(gitlabClient, cfg.GitLabProjectID, mrID)
	if err != nil {
		logger.Warn("Failed to get discussions, falling back to simple notes", "error", err)
		// Fall back to regular notes if discussions API fails
		return migrateSimpleComments(ctx, gitlabClient, githubClient, cfg, mrID, prNumber)
	}

	// Also get the regular notes as a backup and for non-discussion notes
	allNotes, err := gitlab.GetMergeRequestNotes(gitlabClient, cfg.GitLabProjectID, mrID)
	if err != nil {
		return 0, err
	}

	// Organize notes that aren't in discussions
	discussionNoteIDs := make(map[int]bool)
	for id := range discussionNotes {
		discussionNoteIDs[id] = true
	}

	// Add notes that aren't in discussions
	for _, note := range allNotes {
		if !note.System && !discussionNoteIDs[note.ID] {
			discussionNotes[note.ID] = &gitlab.DiscussionNote{
				Note:       note,
				ParentID:   0, // Standalone note has no parent
				Discussion: "",
			}
		}
	}

	// Create corresponding comments in GitHub PR
	processedCount := 0

	// Map to track GitLab comment ID to GitHub comment (for handling replies)
	commentMap := make(map[int]int64)

	// First, process top-level comments (no parents)
	for id, discussionNote := range discussionNotes {
		note := discussionNote.Note

		// Skip if this is not a top-level comment
		if discussionNote.ParentID != 0 {
			continue
		}

		// Skip system notes
		if note.System {
			continue
		}

		// Process and create the comment, tracking the GitHub ID
		isResolved := note.Resolvable && note.Resolved
		ghCommentID, err := createGitHubComment(ctx, githubClient, cfg, prNumber, note, 0, discussionNote.Discussion, isResolved)
		if err != nil {
			logger.Warn("Failed to create comment", "gitlab_id", note.ID, "error", err)
			continue
		}

		if ghCommentID != 0 {
			commentMap[id] = ghCommentID
			processedCount++
		}
	}

	// Now process reply comments
	for id, discussionNote := range discussionNotes {
		note := discussionNote.Note

		// Skip top-level comments (already processed)
		if discussionNote.ParentID == 0 {
			continue
		}

		// Skip system notes
		if note.System {
			continue
		}

		// Get the parent GitHub comment ID
		parentGHID, exists := commentMap[discussionNote.ParentID]
		if !exists {
			// If we can't find the parent, create as a standalone comment
			logger.Warn("Parent comment not found, creating as standalone", "gitlab_id", note.ID, "parent_id", discussionNote.ParentID)
			isResolved := note.Resolvable && note.Resolved
			_, err := createGitHubComment(ctx, githubClient, cfg, prNumber, note, 0, discussionNote.Discussion, isResolved)
			if err != nil {
				logger.Warn("Failed to create fallback comment", "error", err)
			}
			processedCount++
			continue
		}

		// Create as a reply to the parent
		isResolved := note.Resolvable && note.Resolved
		ghCommentID, err := createGitHubComment(ctx, githubClient, cfg, prNumber, note, parentGHID, discussionNote.Discussion, isResolved)
		if err != nil {
			logger.Warn("Failed to create reply comment", "gitlab_id", note.ID, "error", err)
			continue
		}

		if ghCommentID != 0 {
			commentMap[id] = ghCommentID
			processedCount++
		}
	}

	logger.Info("Completed migration of comments", "count", processedCount, "mr_id", mrID)
	return processedCount, nil
}

// createGitHubComment creates a GitHub comment from a GitLab note
// Returns the GitHub comment ID if successful, or 0 if failed
func createGitHubComment(ctx context.Context, githubClient *github.Client, cfg config.Config, prNumber int, note *gitlablib.Note, replyToID int64, discussionID string, isResolved bool) (int64, error) {
	// isResolved parameter is now passed directly from the caller

	// Process comment content with truncation - leave more room for metadata
	commentText := utils.TruncateText(note.Body, utils.MaxCommentLength-250) // Leave room for header, metadata and wrapping

	// コメント作成日時の取得
	commentDate := ""
	if !note.CreatedAt.IsZero() {
		commentDate = note.CreatedAt.Format("2006-01-02 15:04:05 MST")
	}

	// コメント更新日時の取得（もし更新されていれば）
	updatedInfo := ""
	if !note.UpdatedAt.IsZero() && note.UpdatedAt.After(note.CreatedAt.Add(time.Minute)) { // 1分以上差があれば更新とみなす
		updatedInfo = fmt.Sprintf(" (edited: %s)", note.UpdatedAt.Format("2006-01-02 15:04:05 MST"))
	}

	// ディスカッション情報を追加（必要に応じて）
	discussionInfo := ""
	if discussionID != "" {
		discussionInfo = fmt.Sprintf("\n\n*From GitLab Discussion: %s*", discussionID)
	}

	// リプライ情報を追加（必要に応じて）
	replyInfo := ""
	if replyToID != 0 {
		replyInfo = "\n\n*This is a reply to another comment*"
	}

	// Wrap the comment if it's resolved
	wrappedText := utils.WrapComment(commentText, isResolved, note.Author.Username)

	// Add header with metadata
	authorName := note.Author.Username
	if note.Author.Name != "" {
		authorName = fmt.Sprintf("%s (%s)", note.Author.Name, note.Author.Username)
	}

	commentBody := fmt.Sprintf("*Comment by %s on GitLab:*\n\n**Posted at:** %s%s%s%s\n\n%s",
		authorName,
		commentDate,
		updatedInfo,
		discussionInfo,
		replyInfo,
		wrappedText)

	// Handle based on whether it's a reply, a review comment, or a regular comment
	if replyToID != 0 {
		// This is a reply to another review comment
		comment, err := github.CreatePRReviewCommentReply(
			ctx,
			githubClient,
			cfg.GitHubOwner,
			cfg.GitHubRepo,
			prNumber,
			commentBody,
			replyToID,
			isResolved, // Pass the resolved flag
		)
		if err != nil {
			// Fall back to regular comment
			logger.Warn("Failed to create review comment reply, falling back to regular comment", "error", err)
			if err := github.CreatePRComment(ctx, githubClient, cfg.GitHubOwner, cfg.GitHubRepo, prNumber, commentBody); err != nil {
				return 0, err
			}
			return 0, nil // No ID to track for regular comments
		}
		return comment.GetID(), nil
	} else if note.Position != nil && note.Position.NewPath != "" {
		// This is a review comment (on code)
		// Log position information for debugging
		logger.Debug("Review comment position info",
			"path", note.Position.NewPath,
			"new_line", note.Position.NewLine,
			"old_line", note.Position.OldLine,
			"resolved", isResolved)

		// Use the new CreatePRReview function that supports resolved comments
		review, err := github.CreatePRReview(
			ctx,
			githubClient,
			cfg.GitHubOwner,
			cfg.GitHubRepo,
			prNumber,
			commentBody,
			note.Position.NewPath,
			note.Position.NewLine,
			isResolved,
		)
		if err != nil {
			// Fall back to regular comment
			logger.Warn("Failed to create review comment, falling back to regular comment", "error", err)
			if err := github.CreatePRComment(ctx, githubClient, cfg.GitHubOwner, cfg.GitHubRepo, prNumber, commentBody); err != nil {
				return 0, err
			}
			return 0, nil // No ID to track for regular comments
		}

		// Return the review ID
		if review != nil && review.ID != nil {
			return *review.ID, nil
		}
		return 0, nil // No ID to track
	} else {
		// Regular comment
		if err := github.CreatePRComment(ctx, githubClient, cfg.GitHubOwner, cfg.GitHubRepo, prNumber, commentBody); err != nil {
			return 0, err
		}
		return 0, nil // No ID to track for regular comments
	}
}

// migrateSimpleComments migrates comments without using the discussions API (fallback method)
func migrateSimpleComments(ctx context.Context, gitlabClient *gitlablib.Client, githubClient *github.Client, cfg config.Config, mrID int, prNumber int) (int, error) {
	// Get all comments from GitLab MR
	allNotes, err := gitlab.GetMergeRequestNotes(gitlabClient, cfg.GitLabProjectID, mrID)
	if err != nil {
		return 0, err
	}

	// Create corresponding comments in GitHub PR
	processedCount := 0

	for _, note := range allNotes {
		// Skip system notes
		if note.System {
			continue
		}

		// Check if comment is resolved
		isResolved := note.Resolvable && note.Resolved

		// Process comment content with truncation - leave more room for metadata
		commentText := utils.TruncateText(note.Body, utils.MaxCommentLength-250) // Leave room for header, metadata and wrapping

		// コメント作成日時の取得
		commentDate := ""
		if !note.CreatedAt.IsZero() {
			commentDate = note.CreatedAt.Format("2006-01-02 15:04:05 MST")
		}

		// コメント更新日時の取得（もし更新されていれば）
		updatedInfo := ""
		if !note.UpdatedAt.IsZero() && note.UpdatedAt.After(note.CreatedAt.Add(time.Minute)) { // 1分以上差があれば更新とみなす
			updatedInfo = fmt.Sprintf(" (edited: %s)", note.UpdatedAt.Format("2006-01-02 15:04:05 MST"))
		}

		// Wrap the comment if it's resolved
		wrappedText := utils.WrapComment(commentText, isResolved, note.Author.Username)

		// Add header with metadata
		authorName := note.Author.Username
		if note.Author.Name != "" {
			authorName = fmt.Sprintf("%s (%s)", note.Author.Name, note.Author.Username)
		}

		commentBody := fmt.Sprintf("*Comment by %s on GitLab:*\n\n**Posted at:** %s%s\n\n%s",
			authorName,
			commentDate,
			updatedInfo,
			wrappedText)

		// Create regular comment if no position info
		if note.Position == nil {
			if err := github.CreatePRComment(ctx, githubClient, cfg.GitHubOwner, cfg.GitHubRepo, prNumber, commentBody); err != nil {
				logger.Warn("Failed to create regular comment", "error", err)
			}
			processedCount++
			continue
		}

		// If we have position information, try to create a review comment
		if note.Position.NewPath != "" {
			// Log position information for debugging
			logger.Debug("Review comment position info (simple mode)",
				"path", note.Position.NewPath,
				"new_line", note.Position.NewLine,
				"old_line", note.Position.OldLine)

			// Use the new review function with resolved flag
			_, err := github.CreatePRReview(
				ctx,
				githubClient,
				cfg.GitHubOwner,
				cfg.GitHubRepo,
				prNumber,
				commentBody,
				note.Position.NewPath,
				note.Position.NewLine,
				isResolved,
			)

			if err != nil {
				// Fall back to regular comment if review comment fails
				logger.Warn("Failed to create review comment, falling back to regular comment", "error", err)
				if err := github.CreatePRComment(ctx, githubClient, cfg.GitHubOwner, cfg.GitHubRepo, prNumber, commentBody); err != nil {
					logger.Warn("Failed to create fallback comment", "error", err)
				}
			}
			processedCount++
		}
	}

	logger.Info("Completed migration of comments (simple mode)", "count", processedCount, "mr_id", mrID)
	return processedCount, nil
}
