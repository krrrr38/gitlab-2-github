package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/krrrr38/gitlab-2-github/pkg/config"
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	var cfg config.GlobalConfig

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
	rootCmd.PersistentFlags().StringVar(&cfg.GitLabProject, "gitlab-project", "", "GitLab project ID or path (namespace/project-name)")
	rootCmd.PersistentFlags().StringVar(&cfg.GitHubGitToken, "github-git-token", "", "GitHub Git token (or set GITHUB_GIT_TOKEN env)")
	rootCmd.PersistentFlags().StringVar(&cfg.GitHubApiToken, "github-api-token", "", "GitHub API token (or set GITHUB_API_TOKEN env)")
	rootCmd.PersistentFlags().IntVar(&cfg.GitHubAppID, "github-app-id", 0, "GitHub APP ID (or set GITHUB_APP_ID env)")
	rootCmd.PersistentFlags().IntVar(&cfg.GitHubAppInstallationID, "github-app-installation-id", 0, "GitHub APP Installation ID (or set GITHUB_APP_INSTALLATION_ID env)")
	rootCmd.PersistentFlags().StringVar(&cfg.GitHubAppPrivateKey, "github-app-private-key", "", "GitHub APP private key (or set GITHUB_APP_PRIVATE_KEY env)")
	rootCmd.PersistentFlags().BoolVar(&cfg.GitHubAppPrivateKeyAsFile, "github-app-private-key-as-file", false, "GitHub APP private key as file")
	rootCmd.PersistentFlags().StringVar(&cfg.GitHubOwner, "github-owner", "", "GitHub owner (username or organization)")
	rootCmd.PersistentFlags().StringVar(&cfg.GitHubRepo, "github-repo", "", "GitHub repository name")
	rootCmd.PersistentFlags().StringVar(&cfg.WorkingDir, "working-dir", "./tmp", "Working directory for git operations")
	rootCmd.PersistentFlags().StringVar(&cfg.LogLevel, "log-level", "info", "Log level (debug, info, warn, error, fatal)")

	// Use environment variables if flags are not provided
	if cfg.GitLabToken == "" {
		cfg.GitLabToken = os.Getenv("GITLAB_TOKEN")
	}
	if cfg.GitHubGitToken == "" {
		cfg.GitHubGitToken = os.Getenv("GITHUB_GIT_TOKEN")
	}
	if cfg.GitHubApiToken == "" {
		cfg.GitHubApiToken = os.Getenv("GITHUB_API_TOKEN")
	}
	if cfg.GitHubAppID == 0 {
		cfg.GitHubAppID, _ = strconv.Atoi(os.Getenv("GITHUB_APP_ID"))
	}
	if cfg.GitHubAppInstallationID == 0 {
		cfg.GitHubAppInstallationID, _ = strconv.Atoi(os.Getenv("GITHUB_APP_INSTALLATION_ID"))
	}
	if cfg.GitHubAppPrivateKey == "" {
		cfg.GitHubAppPrivateKey = os.Getenv("GITHUB_APP_PRIVATE_KEY")
	}
	if cfg.GitHubAppPrivateKeyAsFile {
		privateKey, err := os.ReadFile(cfg.GitHubAppPrivateKey)
		if err != nil {
			logger.Fatal(fmt.Sprintf("could not read private key: %s", cfg.GitHubAppPrivateKey), "err", err)
		}
		cfg.GitHubAppPrivateKey = string(privateKey)
	}

	// Configure logger based on log level
	if cfg.LogLevel != "" {
		logger.SetLevel(cfg.LogLevel)
	}

	// Add subcommands
	rootCmd.AddCommand(NewMigrateCommand(&cfg))

	return rootCmd
}
