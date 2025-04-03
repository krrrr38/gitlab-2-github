package migration

import (
	"context"
	"fmt"
	"github.com/krrrr38/gitlab-2-github/pkg/config"
	"github.com/krrrr38/gitlab-2-github/pkg/git"
	githubClient "github.com/krrrr38/gitlab-2-github/pkg/github"
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
	"net/url"
)

// checkGitHubRepositoryExists checks if the GitHub repository exists
func checkGitHubRepositoryExists(ctx context.Context, cfg config.GlobalConfig, gh *githubClient.Client) (bool, error) {
	// リポジトリの存在確認
	var exists bool
	err := githubClient.RetryableOperation(ctx, func() error {
		_, resp, err := gh.GetInner().Repositories.Get(ctx, cfg.GitHubOwner, cfg.GitHubRepo)
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
func createGitHubRepository(ctx context.Context, cfg config.GlobalConfig, gh *githubClient.Client) error {
	description := fmt.Sprintf("Migrated from GitLab: %s", cfg.GitLabProject)
	gitlabProjectUrl, _ := url.Parse(fmt.Sprintf("%s/%s", cfg.GitLabURL, cfg.GitLabProject))
	err := githubClient.RetryableOperation(ctx, func() error {
		return githubClient.CreateRepository(ctx, gh, cfg.GitHubOwner, cfg.GitHubRepo, description, gitlabProjectUrl)
	})
	if err != nil {
		return fmt.Errorf("failed to create GitHub repository: %w", err)
	}

	logger.Info("Created new GitHub repository", "owner", cfg.GitHubOwner, "repo", cfg.GitHubRepo)
	return nil
}

// MirrorRepository mirrors a GitLab repository to GitHub
func MirrorRepository(g *git.Git, cfg config.GlobalConfig, gh *githubClient.Client) error {
	ctx := context.Background()

	// GitHubリポジトリの存在確認
	exists, err := checkGitHubRepositoryExists(ctx, cfg, gh)
	if err != nil {
		return err
	}

	// リポジトリが存在しない場合は作成
	if !exists {
		logger.Info("GitHub repository does not exist, creating...", "owner", cfg.GitHubOwner, "repo", cfg.GitHubRepo)
		if err := createGitHubRepository(ctx, cfg, gh); err != nil {
			return err
		}
	}

	if err = g.Init(cfg.GitHubGitToken, cfg.GitLabToken); err != nil {
		return err
	}

	return nil
}
