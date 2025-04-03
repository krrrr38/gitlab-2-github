package cmd

import (
	"context"
	"fmt"
	"github.com/krrrr38/gitlab-2-github/pkg/config"
	"github.com/krrrr38/gitlab-2-github/pkg/git"
	"github.com/krrrr38/gitlab-2-github/pkg/github"
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
	"github.com/krrrr38/gitlab-2-github/pkg/migration"
	"github.com/spf13/cobra"
	"github.com/xanzy/go-gitlab"
	"os"
	"os/signal"
	"syscall"
)

func NewMigrateCommand(cfg *config.GlobalConfig) *cobra.Command {
	var migrateConfig config.MigrateConfig
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate a GitLab project to GitHub",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigration(*cfg, migrateConfig)
		},
	}

	// Migrate command specific flags
	cmd.Flags().IntSliceVar(&migrateConfig.FilterMergeReqIDs, "mr-ids", nil, "Filter specific merge request IDs to migrate")
	cmd.Flags().IntVar(&migrateConfig.ContinueFromMRID, "continue-from", 0, "Continue migration from the specified MR ID")
	cmd.Flags().IntVar(&migrateConfig.MaxDiscussions, "max-discussions", 0, "Max migration discussion count per merge request")

	return cmd
}

func runMigration(cfg config.GlobalConfig, migrateConfig config.MigrateConfig) error {
	// Initialize GitLab client
	gitlabClient, err := gitlab.NewClient(cfg.GitLabToken, gitlab.WithBaseURL(cfg.GitLabURL))
	if err != nil {
		return fmt.Errorf("failed to create GitLab client: %w", err)
	}

	// Initialize GitHub client with retry capability
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// シグナルハンドリングのセットアップ（CTRL+Cなどの割り込みを処理）
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	// シグナルハンドラ
	go func() {
		<-signalChan
		logger.Info("Received interrupt signal, shutting down...")

		// コンテキストをキャンセルして実行中の処理に停止を通知
		cancel()

		os.Exit(0)
	}()

	// リポジトリ設定を取得してミラーリングが必要かどうかを判断
	g := git.NewGit(cfg.WorkingDir, cfg.GitHubOwner, cfg.GitHubRepo, cfg.GitLabURL, cfg.GitLabProject)

	var githubClient *github.Client
	if cfg.GitHubApiToken != "" {
		githubClient = github.NewClientByPAT(cfg.GitHubApiToken)
	} else if cfg.GitHubAppID > 0 && cfg.GitHubAppInstallationID > 0 && cfg.GitHubAppPrivateKey != "" {
		githubClient = github.NewClientByApp(cfg.GitHubAppID, cfg.GitHubAppInstallationID, cfg.GitHubAppPrivateKey)
	} else {
		logger.Fatal("GitHub token or GitHub App settings are required")
	}

	// 1. リポジトリをミラーリング
	logger.Info("Migration started...")
	if err := migration.MirrorRepository(g, cfg, githubClient); err != nil {
		return fmt.Errorf("failed to mirror repository: %w", err)
	}

	// 2. マージリクエストの移行（リクエストされている場合）
	// マイグレーションオプションを設定
	migrationOpts := &migration.MigrationOptions{
		ContinueFromID:    migrateConfig.ContinueFromMRID,
		FilterMergeReqIDs: migrateConfig.FilterMergeReqIDs,
		MaxDiscussions:    migrateConfig.MaxDiscussions,
	}
	if err := migration.MigrateMergeRequests(ctx, gitlabClient, githubClient, cfg, migrationOpts); err != nil {
		return fmt.Errorf("failed to migrate merge requests: %w", err)
	}

	logger.Info("Migration completed successfully!")
	return nil
}
