package migration

import (
	"context"
	"fmt"
	"github.com/google/go-github/v70/github"
	"github.com/krrrr38/gitlab-2-github/pkg/config"
	"github.com/krrrr38/gitlab-2-github/pkg/git"
	githubClient "github.com/krrrr38/gitlab-2-github/pkg/github"
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
)

// checkGitHubRepositoryExists checks if the GitHub repository exists
func checkGitHubRepositoryExists(ctx context.Context, cfg config.GlobalConfig) (bool, error) {
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
func createGitHubRepository(ctx context.Context, cfg config.GlobalConfig) error {
	// GitHubクライアントを初期化
	client := githubClient.NewClient(cfg.GitHubToken)

	// リポジトリ作成リクエスト
	repo := &github.Repository{
		Name:        github.String(cfg.GitHubRepo),
		Private:     github.Bool(true), // デフォルトではプライベートリポジトリとして作成
		Description: github.String(fmt.Sprintf("Migrated from GitLab: %s", cfg.GitLabProject)),
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
func MirrorRepository(g *git.Git, cfg config.GlobalConfig) error {
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

	if err = g.Init(cfg.GitHubToken, cfg.GitLabToken); err != nil {
		return err
	}

	return nil
}
