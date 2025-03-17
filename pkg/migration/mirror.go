package migration

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v60/github"
	"github.com/krrrr38/gitlab-2-github/pkg/config"
	"github.com/krrrr38/gitlab-2-github/pkg/git"
	githubClient "github.com/krrrr38/gitlab-2-github/pkg/github"
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
)

// checkGitHubRepositoryExists checks if the GitHub repository exists
func checkGitHubRepositoryExists(ctx context.Context, cfg config.Config) (bool, error) {
	// GitHubクライアントを初期化
	client := githubClient.NewClient(cfg.GitHubToken)

	// リポジトリの存在確認
	var exists bool
	err := githubClient.RetryableOperation(ctx, func() error {
		_, resp, err := client.GetInner().Repositories.Get(ctx, cfg.GitHubOwner, cfg.GitHubRepo)
		if err != nil {
			if resp != nil && resp.StatusCode == 404 {
				// 404の場合はリポジトリが存在しないだけなのでエラーとしない
				exists = false
				return nil
			}
			return err
		}
		exists = true
		return nil
	})

	if err != nil {
		return false, fmt.Errorf("failed to check GitHub repository: %w", err)
	}

	return exists, nil
}

// createGitHubRepository creates a new GitHub repository
func createGitHubRepository(ctx context.Context, cfg config.Config) error {
	// GitHubクライアントを初期化
	client := githubClient.NewClient(cfg.GitHubToken)

	// リポジトリ作成リクエスト
	repo := &github.Repository{
		Name:        github.String(cfg.GitHubRepo),
		Private:     github.Bool(true), // デフォルトではプライベートリポジトリとして作成
		Description: github.String(fmt.Sprintf("Migrated from GitLab: %s", cfg.GitLabProjectID)),
	}

	// 組織かユーザーかによって呼び出すAPIが異なる
	var err error
	err = githubClient.RetryableOperation(ctx, func() error {
		// 組織の場合
		_, _, err = client.GetInner().Repositories.Create(ctx, cfg.GitHubOwner, repo)
		return err
	})

	if err != nil {
		// 組織として作成に失敗した場合、ユーザーとして作成を試みる
		err = githubClient.RetryableOperation(ctx, func() error {
			_, _, err = client.GetInner().Repositories.Create(ctx, "", repo)
			return err
		})

		if err != nil {
			return fmt.Errorf("failed to create GitHub repository: %w", err)
		}
	}

	logger.Info("Created new GitHub repository", "owner", cfg.GitHubOwner, "repo", cfg.GitHubRepo)
	return nil
}

// MirrorRepository mirrors a GitLab repository to GitHub
func MirrorRepository(cfg config.Config) error {
	ctx := context.Background()

	// GitHubリポジトリの存在確認
	exists, err := checkGitHubRepositoryExists(ctx, cfg)
	if err != nil {
		return err
	}

	// リポジトリが存在しない場合は作成
	if !exists {
		logger.Info("GitHub repository does not exist, creating...", "owner", cfg.GitHubOwner, "repo", cfg.GitHubRepo)
		if err := createGitHubRepository(ctx, cfg); err != nil {
			return err
		}
	}

	// Create temp directory if it doesn't exist
	if err := git.CreateDirectory(cfg.TempDir); err != nil {
		return err
	}

	// Clone GitLab repository with mirror option
	repoDir := fmt.Sprintf("%s/repo", cfg.TempDir)
	if err := git.CleanupDirectory(repoDir); err != nil {
		return fmt.Errorf("failed to clean up temp directory: %w", err)
	}

	// Build GitLab repository URL with token
	gitlabURL := fmt.Sprintf("https://oauth2:%s@%s/%s.git",
		cfg.GitLabToken,
		strings.TrimPrefix(cfg.GitLabURL, "https://"),
		cfg.GitLabProjectID)

	// Build GitHub repository URL with token
	githubURL := fmt.Sprintf("https://%s@github.com/%s/%s.git",
		cfg.GitHubToken,
		cfg.GitHubOwner,
		cfg.GitHubRepo)

	// Clone mirror from GitLab
	logger.Info("Cloning repository from GitLab...")
	cloneCmd := fmt.Sprintf("git clone --mirror %s %s", gitlabURL, repoDir)
	if err := git.ExecuteCommand(cloneCmd); err != nil {
		return fmt.Errorf("failed to clone GitLab repository: %w", err)
	}

	// Push mirror to GitHub
	logger.Info("Pushing repository to GitHub...")
	pushCmd := fmt.Sprintf("cd %s && git push --mirror %s", repoDir, githubURL)
	if err := git.ExecuteCommand(pushCmd); err != nil {
		return fmt.Errorf("failed to push to GitHub: %w", err)
	}

	return nil
}
