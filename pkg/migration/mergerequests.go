package migration

import (
	"context"
	"fmt"
	"sort"
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
		sourceBranchName := fmt.Sprintf("gitlab-mr-%d", mr.IID)
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
			Head: fmt.Sprintf("%s:%s", owner, branchName),
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

	// 0. Recreate target branch if needed (from the correct commit)
	targetBranch := mr.TargetBranch
	
	// 1. Create source branch based on the MR source commit
	sourceBranch := fmt.Sprintf("gitlab-mr-%d", mr.IID)
	
	// Get the correct commit SHA for the source branch
	sourceCommit, _, err := gitlabClient.Commits.GetCommit(cfg.GitLabProjectID, mr.SHA)
	if err != nil {
		return 0, fmt.Errorf("failed to get source commit: %w", err)
	}

	// Create source branch from this commit
	createSourceCmd := fmt.Sprintf("cd %s && git checkout -b %s %s", 
		mrDir, sourceBranch, sourceCommit.ID)
	if err := git.ExecuteCommand(createSourceCmd); err != nil {
		return 0, fmt.Errorf("failed to create source branch: %w", err)
	}

	// 2. Create GitHub PR
	// Prepare PR title (with truncation if needed)
	title := utils.TruncateText(mr.Title, utils.MaxPRTitleLength)
	if mr.State == "closed" {
		// Add [closed] suffix but ensure we don't exceed the limit
		closedSuffix := " [closed]"
		if len(title) + len(closedSuffix) > utils.MaxPRTitleLength {
			title = utils.TruncateText(title, utils.MaxPRTitleLength - len(closedSuffix))
		}
		title += closedSuffix
	}
	
	// Prepare PR body/description with metadata (with truncation if needed)
	// GitLabのMRの詳細情報をヘッダーとして追加
	authorInfo := ""
	if mr.Author != nil {
		authorInfo = fmt.Sprintf("Created by **%s** (%s)", mr.Author.Name, mr.Author.Username)
	}
	
	// 日時情報の取得
	createdAt := ""
	if !mr.CreatedAt.IsZero() {
		createdAt = mr.CreatedAt.Format("2006-01-02 15:04:05 MST")
	}
	
	// Leave room for header (around 200-300 chars)
	description := utils.TruncateText(mr.Description, utils.MaxPRDescriptionLength - 300)
	
	// 説明文にメタデータを含めたヘッダーを追加
	body := fmt.Sprintf("*Migrated from GitLab MR #%d*\n\n**Original Information:**\n- %s\n- Created at: %s\n- URL: %s\n\n---\n\n%s", 
		mr.IID, 
		authorInfo,
		createdAt,
		mr.WebURL,
		description)

	pr, err := github.CreatePullRequest(
		ctx, 
		githubClient, 
		cfg.GitHubOwner, 
		cfg.GitHubRepo, 
		title,
		body,
		sourceBranch, 
		targetBranch,
		mr,
	)
	if err != nil {
		return 0, err
	}

	// 3. Migrate comments
	commentCount := 0
	commentCount, err = migrateComments(ctx, gitlabClient, githubClient, cfg, mr, pr)
	if err != nil {
		logger.Warn("Failed to migrate comments for MR", "id", mr.IID, "error", err)
	}

	// 4. Close PR if original MR was closed or merged
	if mr.State == "closed" || mr.State == "merged" {
		if err := github.ClosePullRequest(
			ctx, 
			githubClient, 
			cfg.GitHubOwner, 
			cfg.GitHubRepo, 
			pr.GetNumber(), 
			mr.State == "merged",
			mr.IID,
		); err != nil {
			return commentCount, err
		}
	}

	return commentCount, nil
}

// migrateComments migrates comments from a GitLab merge request to a GitHub pull request
// 戻り値として移行したコメント数も返す
func migrateComments(ctx context.Context, gitlabClient *gitlablib.Client, githubClient *github.Client, cfg config.Config, mr *gitlablib.MergeRequest, pr *githublib.PullRequest) (int, error) {
	// Get discussions from GitLab MR to track comment relationships
	discussionNotes, err := gitlab.GetMergeRequestDiscussions(gitlabClient, cfg.GitLabProjectID, mr.IID)
	if err != nil {
		logger.Warn("Failed to get discussions, falling back to simple notes", "error", err)
		// Fall back to regular notes if discussions API fails
		return migrateSimpleComments(ctx, gitlabClient, githubClient, cfg, mr, pr)
	}
	
	// Also get the regular notes as a backup and for non-discussion notes
	allNotes, err := gitlab.GetMergeRequestNotes(gitlabClient, cfg.GitLabProjectID, mr.IID)
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
		ghCommentID, err := createGitHubComment(ctx, githubClient, cfg, pr, note, 0, discussionNote.Discussion)
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
			_, err := createGitHubComment(ctx, githubClient, cfg, pr, note, 0, discussionNote.Discussion)
			if err != nil {
				logger.Warn("Failed to create fallback comment", "error", err)
			}
			processedCount++
			continue
		}
		
		// Create as a reply to the parent
		ghCommentID, err := createGitHubComment(ctx, githubClient, cfg, pr, note, parentGHID, discussionNote.Discussion)
		if err != nil {
			logger.Warn("Failed to create reply comment", "gitlab_id", note.ID, "error", err)
			continue
		}
		
		if ghCommentID != 0 {
			commentMap[id] = ghCommentID
			processedCount++
		}
	}
	
	logger.Info("Completed migration of comments", "count", processedCount, "mr_id", mr.IID)
	return processedCount, nil
}

// createGitHubComment creates a GitHub comment from a GitLab note
// Returns the GitHub comment ID if successful, or 0 if failed
func createGitHubComment(ctx context.Context, githubClient *github.Client, cfg config.Config, pr *githublib.PullRequest, note *gitlablib.Note, replyToID int64, discussionID string) (int64, error) {
	// Check if comment is resolved
	isResolved := false
	if note.Resolvable && note.Resolved {
		isResolved = true
	}
	
	// Process comment content with truncation - leave more room for metadata
	commentText := utils.TruncateText(note.Body, utils.MaxCommentLength - 250) // Leave room for header, metadata and wrapping
	
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
			pr.GetNumber(), 
			commentBody, 
			replyToID,
		)
		if err != nil {
			// Fall back to regular comment
			logger.Warn("Failed to create review comment reply, falling back to regular comment", "error", err)
			if err := github.CreatePRComment(ctx, githubClient, cfg.GitHubOwner, cfg.GitHubRepo, pr.GetNumber(), commentBody); err != nil {
				return 0, err
			}
			return 0, nil // No ID to track for regular comments
		}
		return comment.GetID(), nil
	} else if note.Position != nil && note.Position.NewPath != "" {
		// This is a review comment (on code)
		comment, err := github.CreatePRReviewComment(
			ctx, 
			githubClient, 
			cfg.GitHubOwner, 
			cfg.GitHubRepo, 
			pr.GetNumber(), 
			commentBody, 
			note.Position.NewPath, 
			note.Position.NewLine,
		)
		if err != nil {
			// Fall back to regular comment
			logger.Warn("Failed to create review comment, falling back to regular comment", "error", err)
			if err := github.CreatePRComment(ctx, githubClient, cfg.GitHubOwner, cfg.GitHubRepo, pr.GetNumber(), commentBody); err != nil {
				return 0, err
			}
			return 0, nil // No ID to track for regular comments
		}
		return comment.GetID(), nil
	} else {
		// Regular comment
		if err := github.CreatePRComment(ctx, githubClient, cfg.GitHubOwner, cfg.GitHubRepo, pr.GetNumber(), commentBody); err != nil {
			return 0, err
		}
		return 0, nil // No ID to track for regular comments
	}
}

// migrateSimpleComments migrates comments without using the discussions API (fallback method)
func migrateSimpleComments(ctx context.Context, gitlabClient *gitlablib.Client, githubClient *github.Client, cfg config.Config, mr *gitlablib.MergeRequest, pr *githublib.PullRequest) (int, error) {
	// Get all comments from GitLab MR
	allNotes, err := gitlab.GetMergeRequestNotes(gitlabClient, cfg.GitLabProjectID, mr.IID)
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
		isResolved := false
		if note.Resolvable && note.Resolved {
			isResolved = true
		}
		
		// Process comment content with truncation - leave more room for metadata
		commentText := utils.TruncateText(note.Body, utils.MaxCommentLength - 250) // Leave room for header, metadata and wrapping
		
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
			if err := github.CreatePRComment(ctx, githubClient, cfg.GitHubOwner, cfg.GitHubRepo, pr.GetNumber(), commentBody); err != nil {
				logger.Warn("Failed to create regular comment", "error", err)
			}
			processedCount++
			continue
		}
		
		// If we have position information, try to create a review comment
		if note.Position.NewPath != "" {
			_, err := github.CreatePRReviewComment(
				ctx, 
				githubClient, 
				cfg.GitHubOwner, 
				cfg.GitHubRepo, 
				pr.GetNumber(), 
				commentBody, 
				note.Position.NewPath, 
				note.Position.NewLine,
			)
			
			if err != nil {
				// Fall back to regular comment if review comment fails
				logger.Warn("Failed to create review comment, falling back to regular comment", "error", err)
				if err := github.CreatePRComment(ctx, githubClient, cfg.GitHubOwner, cfg.GitHubRepo, pr.GetNumber(), commentBody); err != nil {
					logger.Warn("Failed to create fallback comment", "error", err)
				}
			}
			processedCount++
		}
	}
	
	logger.Info("Completed migration of comments (simple mode)", "count", processedCount, "mr_id", mr.IID)
	return processedCount, nil
}