package migration

import (
	"context"
	"errors"
	"fmt"
	githublib "github.com/google/go-github/v70/github"
	"github.com/krrrr38/gitlab-2-github/pkg/config"
	"github.com/krrrr38/gitlab-2-github/pkg/git"
	"github.com/krrrr38/gitlab-2-github/pkg/github"
	"github.com/krrrr38/gitlab-2-github/pkg/gitlab"
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
	"github.com/krrrr38/gitlab-2-github/pkg/utils"
	gitlablib "github.com/xanzy/go-gitlab"
	"sort"
	"strconv"
	"strings"
)

// MigrateMergeRequests migrates GitLab merge requests to GitHub pull requests
func MigrateMergeRequests(ctx context.Context, gitlabClient *gitlablib.Client, githubClient *github.Client, cfg config.GlobalConfig, opts *MigrationOptions) error {
	g := git.NewGit(cfg.WorkingDir, cfg.GitHubOwner, cfg.GitHubRepo, cfg.GitLabURL, cfg.GitLabProject)

	// Get all merge requests or filter by IDs
	allMRs, err := gitlab.GetMergeRequests(gitlabClient, cfg.GitLabProject, opts.FilterMergeReqIDs)
	if err != nil {
		return fmt.Errorf("failed to get merge requests: %w", err)
	}
	allClosedPRTitles, err := githubClient.GetClosedPullRequestTitles(ctx, cfg.GitHubOwner, cfg.GitHubRepo)
	migratedMRIIDs := make(map[int]struct{})
	for _, title := range allClosedPRTitles {
		// "GL#<mr.IID> " で始まっているものがあれば、migratedMRIIDsに追加
		if strings.HasPrefix(title, "GL#") {
			mrIIDStr := strings.Split(strings.TrimPrefix(title, "GL#"), " ")[0]
			mrIID, _ := strconv.Atoi(mrIIDStr)
			migratedMRIIDs[mrIID] = struct{}{}
		}
	}

	logger.Debug("Found merge requests", "count", len(allMRs))

	// 処理順序を決定（IIDで昇順ソート）
	sort.Slice(allMRs, func(i, j int) bool {
		return allMRs[i].IID < allMRs[j].IID
	})

	var totalProcessed, totalSucceeded, totalFailed int

	targetMRs := make([]*gitlablib.MergeRequest, 0)
	for _, mr := range allMRs {
		if opts.ContinueFromID > 0 && mr.IID < opts.ContinueFromID {
			logger.Debug("Skipping MR (before continue-from point)", "id", mr.IID, "title", mr.Title)
			continue
		}

		// 既に GitHub 側でプルリクエストが存在するかを確認して、あればスキップする
		_, alreadyMigrated := migratedMRIIDs[mr.IID]
		if alreadyMigrated {
			logger.Debug("Skipping already migrated MR", "id", mr.IID, "title", mr.Title)
			continue
		}

		targetMRs = append(targetMRs, mr)
	}

	// For each merge request, create corresponding branches and PR in GitHub
	for _, mr := range targetMRs {
		// コンテキストが既にキャンセルされていないか確認
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// 処理を継続
		}

		logger.Info("Migrating MR", "id", mr.IID, "title", mr.Title)

		// Get detailed MR information
		detailedMR, _, err := gitlabClient.MergeRequests.GetMergeRequest(cfg.GitLabProject, mr.IID, nil)
		if err != nil {
			logger.Warn("Failed to get detailed info for MR", "id", mr.IID, "error", err)
			totalProcessed++
			totalFailed++
			continue
		}

		// Create branches and PR in GitHub
		err = processMergeRequest(ctx, gitlabClient, githubClient, cfg, detailedMR, g)
		if err != nil {
			logger.Warn("Failed to migrate MR", "id", mr.IID, "error", err)
			totalProcessed++
			totalFailed++
		} else {
			totalProcessed++
			totalSucceeded++
		}

		// 進捗状況を表示
		logger.Info("Progress",
			"processed", totalProcessed,
			"target", len(targetMRs),
			"succeeded", totalSucceeded,
			"failed", totalFailed)
	}

	// 最終の統計情報を表示
	logger.Info("Migration completed",
		"processed", totalProcessed,
		"target", len(targetMRs),
		"succeeded", totalSucceeded,
		"failed", totalFailed)

	return nil
}

// processMergeRequest handles the migration of a single merge request
func processMergeRequest(ctx context.Context, gitlabClient *gitlablib.Client, githubClient *github.Client, cfg config.GlobalConfig, mr *gitlablib.MergeRequest, g *git.Git) error {
	// Prepare unique branch names for both source and target
	sourceBranch := fmt.Sprintf("gitlab-mr-%d-source", mr.IID)
	targetBranch := fmt.Sprintf("gitlab-mr-%d-target", mr.IID)
	defer func() {
		// Delete source branch
		err := githubClient.DeleteBranch(ctx, cfg.GitHubOwner, cfg.GitHubRepo, sourceBranch)
		if err != nil {
			logger.Warn("Failed to delete source branch", "branch", sourceBranch, "error", err)
		}
		err = githubClient.DeleteBranch(ctx, cfg.GitHubOwner, cfg.GitHubRepo, targetBranch)
		if err != nil {
			logger.Warn("Failed to delete temporary target branch", "branch", targetBranch, "error", err)
		}
	}()

	pr, err := createPullRequest(ctx, gitlabClient, githubClient, cfg, mr, sourceBranch, targetBranch, g)
	if err != nil {
		return fmt.Errorf("failed to create PR: %w", err)
	}
	if err := migratePullRequestComments(ctx, gitlabClient, githubClient, cfg, mr, pr); err != nil {
		logger.Warn("Failed to migrate some comments", "error", err)
		// Continue despite comment migration errors
	}

	if mr.State == "closed" {
		err = githubClient.AddLabelsToIssue(ctx, cfg.GitHubOwner, cfg.GitHubRepo, pr.GetNumber(), []string{"closed"})
		if err != nil {
			logger.Warn("Failed to add pr closed labels", "error", err)
		}
	} else if mr.State == "merged" {
		err = githubClient.AddLabelsToIssue(ctx, cfg.GitHubOwner, cfg.GitHubRepo, pr.GetNumber(), []string{"merged"})
		if err != nil {
			logger.Warn("Failed to add pr merged labels", "error", err)
		}
	}

	// 4. Close the PR if the original MR was closed/merged
	if mr.State == "closed" || mr.State == "merged" {
		err = github.RetryableOperation(ctx, func() error {
			return githubClient.ClosePullRequest(ctx, cfg.GitHubOwner, cfg.GitHubRepo, pr.GetNumber())
		})

		if err != nil {
			logger.Warn("Failed to close PR", "error", err)
		} else {
			logger.Debug("Closed GitHub PR", "number", pr.GetNumber())
		}
	}
	return nil
}

func createPullRequest(ctx context.Context, gitlabClient *gitlablib.Client, githubClient *github.Client, cfg config.GlobalConfig, mr *gitlablib.MergeRequest, sourceBranch, targetBranch string, g *git.Git) (*githublib.PullRequest, error) {
	logger.Debug("Creating unique branches for migration", "mr", mr.IID, "source", sourceBranch, "target", targetBranch)

	// Check if DiffRefs are available (use proper field names)
	if mr.DiffRefs.BaseSha == "" || mr.DiffRefs.HeadSha == "" {
		return nil, fmt.Errorf("missing DiffRefs information in merge request %d", mr.IID)
	}

	// Log the SHA values for debugging
	logger.Debug("Using DiffRefs for exact commit matching",
		"mr_id", mr.IID,
		"base_sha", mr.DiffRefs.BaseSha,
		"head_sha", mr.DiffRefs.HeadSha)

	// Create target/source branch from sha
	if err := g.CreateBranch(targetBranch, mr.DiffRefs.BaseSha); err != nil {
		return nil, fmt.Errorf("failed to create target branch: %w", err)
	}
	if err := g.CreateBranch(sourceBranch, mr.DiffRefs.HeadSha); err != nil {
		return nil, fmt.Errorf("failed to create target branch: %w", err)
	}
	if err := g.PushBranchOrigin(targetBranch); err != nil {
		return nil, fmt.Errorf("failed to push branch: %w", err)
	}
	if err := g.PushBranchOrigin(sourceBranch); err != nil {
		return nil, fmt.Errorf("failed to push branch: %w", err)
	}

	// Create GitHub PR
	// Prepare PR title (移行済みかどうかのmappingのために "GL#<mr.IID> " を付与)
	var title string
	if mr.State == "closed" {
		title = "[Closed] " + title
	} else {
		title = fmt.Sprintf("GL#%d %s", mr.IID, mr.Title)
	}
	truncatedTitle := utils.TruncateText(title, utils.MaxPRTitleLength)
	// マージリクエストの承認情報を取得
	approvals, err := gitlab.GetMergeRequestApprovals(gitlabClient, cfg.GitLabProject, mr.IID)
	if err != nil {
		logger.Warn("Failed to get MR approvals", "error", err)
		// エラーがあっても処理は続行
	}

	// 承認情報をフォーマット
	var approvalsText string
	if len(approvals) > 0 {
		approvalsText = ""
		for _, approval := range approvals {
			approvalsText += fmt.Sprintf("- Approved by `%s` on %s\n",
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
	body := fmt.Sprintf("%s\n\n<details><summary>%s Created GitLab Merge Request</summary>\n\n"+
		"**Original MR:** %s/%s/merge_requests/%d\n"+
		"**Created:** %s\n"+
		"**Status:** %s\n"+
		"**Approvals:** \n%s\n</details>",
		description,
		mr.Author.Username,
		cfg.GitLabURL, cfg.GitLabProject, mr.IID,
		createdAt,
		mr.State,
		approvalsText)

	body = utils.TruncateText(body, utils.MaxPRDescriptionLength)

	// Create the PR
	var pr *githublib.PullRequest
	err = github.RetryableOperation(ctx, func() error {
		var err error
		pr, err = githubClient.CreatePullRequest(ctx, cfg.GitHubOwner, cfg.GitHubRepo, &github.PullRequestOptions{
			Title:               truncatedTitle,
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
		var noDiffErr *github.NoDiffError
		if errors.As(err, &noDiffErr) {
			logger.Debug("No difference ignored", "source", noDiffErr.Head, "target", noDiffErr.Base)
		} else {
			return nil, fmt.Errorf("failed to create GitHub PR: %w", err)
		}
	}

	logger.Info("Created GitHub PR", "number", pr.GetNumber(), "url", pr.GetHTMLURL(), "mr", mr.WebURL)
	return pr, nil
}

// migrateComments migrates comments from a GitLab merge request to a GitHub pull request
func migratePullRequestComments(ctx context.Context, gitlabClient *gitlablib.Client, githubClient *github.Client, cfg config.GlobalConfig, mr *gitlablib.MergeRequest, pr *githublib.PullRequest) error {
	// Get discussions from GitLab MR to track comment relationships
	discussions, err := gitlab.GetMergeRequestDiscussions(gitlabClient, cfg.GitLabProject, mr.IID)
	if err != nil {
		return fmt.Errorf("failed to get discussions: %w on mr.IID=%d", err, mr.IID)
	}

	// Create corresponding comments in GitHub PR
	processedCount := 0

	for _, discussion := range discussions {
		err = createGitHubDiscussion(ctx, githubClient, cfg, mr, pr, discussion)
		if err != nil {
			logger.Warn(fmt.Sprintf("Failed to create comment: %v", discussion), "error", err)
			continue
		}
	}

	logger.Debug("Completed migration of comments", "count", processedCount, "mr_id", mr.IID)
	return nil
}

// createGitHubComments creates a GitHub comment from a GitLab note
func createGitHubDiscussion(ctx context.Context, githubClient *github.Client, cfg config.GlobalConfig, mr *gitlablib.MergeRequest, pr *githublib.PullRequest, discussion *gitlablib.Discussion) error {
	headNote := discussion.Notes[0]
	tailNotes := discussion.Notes[1:]

	if headNote.System {
		// 以下のようなcommit hashを持つsystem commentの場合、そのcommitにPRへのリンクをコメントする
		// この対応を行わないと、移行に際してcommitから参考となるPRが引けなくなるため。
		// "mentioned in commit 21bff6b64c0ecaacb0cecf09b9f1c662f9e62b21"
		if strings.Contains(headNote.Body, "mentioned in commit ") {
			commitHash := strings.TrimPrefix(headNote.Body, "mentioned in commit ")
			body := fmt.Sprintf("Related PR: [%s](%s)", pr.GetTitle(), pr.GetHTMLURL())
			err := githubClient.CreateCommitComment(ctx, cfg.GitHubOwner, cfg.GitHubRepo, commitHash, body)
			if err != nil {
				// エラーが出てもスルー
				logger.Warn(fmt.Sprintf("Failed to create commit comment: %v", headNote), "error", err)
				return nil
			}
		}

		// ignore unused system comment
		if strings.Contains(headNote.Body, "addded ") || strings.Contains(headNote.Body, "approved this merge request") || strings.Contains(headNote.Body, "requested review") || strings.Contains(headNote.Body, "resolved all threads") || strings.Contains(headNote.Body, "mentioned in commit ") {
			return nil
		}

		body := fmt.Sprintf("【system】%s", headNote.Body)
		_, err := githubClient.CreateIssueComment(ctx, cfg.GitHubOwner, cfg.GitHubRepo, pr.GetNumber(), body, headNote.Resolved)
		if err != nil {
			return err
		}

		return nil
	}

	var headCommentID int64
	var hasPRComment bool
	if discussion.IndividualNote || headNote.Position == nil {
		// 個別のコメントの場合は、そのままIssueCommentとする
		comment, err := githubClient.CreateIssueComment(ctx, cfg.GitHubOwner, cfg.GitHubRepo, pr.GetNumber(), formatGitHubCommentBody(headNote), headNote.Resolved)
		if err != nil {
			return fmt.Errorf("failed to create head issue comment: %w, note=%v", err, headNote)
		}
		headCommentID = comment.GetID()
	} else {
		// Review Commentの場合は、対象のファイルや位置情報を持つ
		// Discussionの先頭となるコメントを作成　(スレが無いコメントの場合、こちらのみ作成される)
		headNoteStartLine, headNoteEndLine := resolveCommentLineRanges(headNote)
		headCommentInput := &github.CreatePRCommentInput{
			Owner:     cfg.GitHubOwner,
			Repo:      cfg.GitHubRepo,
			PrNumber:  pr.GetNumber(),
			Body:      formatGitHubCommentBody(headNote),
			Path:      headNote.Position.NewPath,
			Sha1:      mr.DiffRefs.HeadSha,
			Resolved:  headNote.Resolved,
			StartLine: headNoteStartLine,
			LastLine:  headNoteEndLine,
		}
		headComment, err := githubClient.CreatePRComment(ctx, headCommentInput)
		if err != nil {
			// PRのdiff hunk外のコメントなどはエラーになってしまうため、Issue Commentにfallbackさせる
			comment, err := githubClient.CreateIssueComment(ctx, cfg.GitHubOwner, cfg.GitHubRepo, pr.GetNumber(), formatGitHubCommentBody(headNote), headNote.Resolved)
			if err != nil {
				return fmt.Errorf("failed to create head issue comment: %w, note=%v", err, headNote)
			}
			headCommentID = comment.GetID()
		} else {
			headCommentID = headComment.GetID()
			hasPRComment = true
		}
	}

	var replyIssueComment = ""
	for _, note := range tailNotes {
		if note.System {
			continue
		}

		if hasPRComment {
			// // PR Review Commentと出来た場合にはreplyをする
			replyInput := &github.CreatePRCommentReplyInput{
				Owner:     cfg.GitHubOwner,
				Repo:      cfg.GitHubRepo,
				PrNumber:  pr.GetNumber(),
				Body:      formatGitHubCommentBody(note),
				Resolved:  note.Resolved,
				CommentID: headCommentID, // reply先となるコメント
			}
			if err := githubClient.CreatePRCommentReply(ctx, replyInput); err != nil {
				return err
			}
		} else {
			// そうでないなら、replyは出来ないため、集約してIssueCommentとする
			replyIssueComment += formatGitHubCommentBody(note) + "\n\n----\n"
		}
	}
	if !hasPRComment && replyIssueComment != "" {
		commentText := utils.TruncateText(replyIssueComment, utils.MaxCommentLength)
		_, err := githubClient.CreateIssueComment(ctx, cfg.GitHubOwner, cfg.GitHubRepo, pr.GetNumber(), commentText, true)
		if err != nil {
			return fmt.Errorf("failed to create tail issue comments: %w, note=%v", err, headNote)
		}
	}
	return nil
}

func resolveCommentLineRanges(note *gitlablib.Note) (*int, *int) {
	var numbers []int
	if note.Position != nil && note.Position.LineRange != nil {
		if note.Position.LineRange.StartRange != nil {
			if note.Position.LineRange.StartRange.OldLine != 0 {
				numbers = append(numbers, note.Position.LineRange.StartRange.OldLine)
			}
			if note.Position.LineRange.StartRange.NewLine != 0 {
				numbers = append(numbers, note.Position.LineRange.StartRange.NewLine)
			}
		}
		if note.Position.LineRange.EndRange != nil {
			if note.Position.LineRange.EndRange.OldLine != 0 {
				numbers = append(numbers, note.Position.LineRange.EndRange.OldLine)
			}
			if note.Position.LineRange.EndRange.NewLine != 0 {
				numbers = append(numbers, note.Position.LineRange.EndRange.NewLine)
			}
		}
	}
	sort.SliceStable(numbers, func(i, j int) bool {
		return numbers[i] < numbers[j]
	})
	if len(numbers) > 0 {
		from := numbers[0]
		to := numbers[len(numbers)-1]
		return &from, &to
	}
	return nil, nil
}

func formatGitHubCommentBody(note *gitlablib.Note) string {
	commentText := utils.TruncateText(note.Body, utils.MaxCommentLength)
	commentDate := ""
	if !note.CreatedAt.IsZero() {
		commentDate = note.CreatedAt.Format("2006-01-02 15:04:05 MST")
	}
	// Add header with metadata
	authorName := note.Author.Username
	if note.Author.Name != "" {
		authorName = fmt.Sprintf("%s (%s)", note.Author.Name, note.Author.Username)
	}
	commentBody := fmt.Sprintf("%s\nby `%s` at `%s`",
		commentText,
		authorName,
		commentDate,
	)
	return commentBody
}
