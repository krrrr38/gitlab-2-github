package cmd

import (
	"os"

	"github.com/krrrr38/gitlab-2-github/pkg/config"
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	var cfg config.Config

	rootCmd := &cobra.Command{
		Use:   "gitlab-2-github",
		Short: "Migrate GitLab repositories to GitHub including merge requests",
		Long: `Migrate GitLab repositories to GitHub including merge requests.
This tool performs:
- Repository mirroring with branches and tags
- Migration of merge requests to GitHub pull requests 
- Pull request description and comment migration`,
	}

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfg.GitLabToken, "gitlab-token", "", "GitLab API token (or set GITLAB_TOKEN env)")
	rootCmd.PersistentFlags().StringVar(&cfg.GitLabURL, "gitlab-url", "https://gitlab.com", "GitLab URL")
	rootCmd.PersistentFlags().StringVar(&cfg.GitLabProjectID, "gitlab-project", "", "GitLab project ID or path (namespace/project-name)")
	rootCmd.PersistentFlags().StringVar(&cfg.GitHubToken, "github-token", "", "GitHub API token (or set GITHUB_TOKEN env)")
	rootCmd.PersistentFlags().StringVar(&cfg.GitHubOwner, "github-owner", "", "GitHub owner (username or organization)")
	rootCmd.PersistentFlags().StringVar(&cfg.GitHubRepo, "github-repo", "", "GitHub repository name")
	rootCmd.PersistentFlags().StringVar(&cfg.TempDir, "temp-dir", "./tmp", "Temporary directory for git operations")
	rootCmd.PersistentFlags().StringVar(&cfg.LogLevel, "log-level", "info", "Log level (debug, info, warn, error, fatal)")

	// Use environment variables if flags are not provided
	if cfg.GitLabToken == "" {
		cfg.GitLabToken = os.Getenv("GITLAB_TOKEN")
	}
	if cfg.GitHubToken == "" {
		cfg.GitHubToken = os.Getenv("GITHUB_TOKEN")
	}

	// Configure logger based on log level
	if cfg.LogLevel != "" {
		logger.SetLevel(cfg.LogLevel)
	}

	// Add subcommands
	rootCmd.AddCommand(NewMigrateCommand(&cfg))

	return rootCmd
}
