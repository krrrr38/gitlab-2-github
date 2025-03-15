package migration

import (
	"fmt"
	"strings"

	"github.com/krrrr38/gitlab-2-github/pkg/config"
	"github.com/krrrr38/gitlab-2-github/pkg/git"
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
)

// MirrorRepository mirrors a GitLab repository to GitHub
func MirrorRepository(cfg config.Config) error {
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