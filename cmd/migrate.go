package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/krrrr38/gitlab-2-github/pkg/config"
	"github.com/krrrr38/gitlab-2-github/pkg/github"
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
	"github.com/krrrr38/gitlab-2-github/pkg/migration"
	"github.com/spf13/cobra"
	"github.com/xanzy/go-gitlab"
)

func NewMigrateCommand(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate a GitLab project to GitHub",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigration(*cfg)
		},
	}

	// Migrate command specific flags
	cmd.Flags().BoolVar(&cfg.IncludePRs, "include-prs", true, "Migrate merge requests to pull requests")
	cmd.Flags().IntSliceVar(&cfg.FilterMergeReqIDs, "mr-ids", nil, "Filter specific merge request IDs to migrate")
	cmd.Flags().IntVar(&cfg.ContinueFromMRID, "continue-from", 0, "Continue migration from the specified MR ID")

	return cmd
}

// checkGitHubRepoExists checks if a GitHub repository exists and has content
func checkGitHubRepoExists(ctx context.Context, githubClient *github.Client, owner, repo string) (bool, error) {
	// リポジトリの情報を取得
	err := github.RetryableOperation(ctx, func() error {
		_, _, err := githubClient.GetInner().Repositories.Get(ctx, owner, repo)
		return err
	})
	
	if err != nil {
		return false, err
	}
	
	// リポジトリは存在するが、コミットがあるかを確認
	var hasCommits bool
	err = github.RetryableOperation(ctx, func() error {
		commits, _, err := githubClient.GetInner().Repositories.ListCommits(ctx, owner, repo, nil)
		if err != nil {
			return err
		}
		
		hasCommits = len(commits) > 0
		return nil
	})
	
	if err != nil {
		return false, err
	}
	
	return hasCommits, nil
}

func runMigration(cfg config.Config) error {
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
	
	githubClient := github.NewClient(cfg.GitHubToken)
	
	// リポジトリ設定を取得してミラーリングが必要かどうかを判断
	// GitHubリポジトリが存在し、少なくとも1つのコミットがあれば既にミラーリング済みと見なす
	repoExists, err := checkGitHubRepoExists(ctx, githubClient, cfg.GitHubOwner, cfg.GitHubRepo)
	if err != nil {
		logger.Warn("Failed to check GitHub repository status", "error", err)
	}
	
	if !repoExists {
		// 1. リポジトリをミラーリング
		logger.Info("Mirroring repository...")
		if err := migration.MirrorRepository(cfg); err != nil {
			return fmt.Errorf("failed to mirror repository: %w", err)
		}
	} else {
		logger.Info("Repository already exists on GitHub, skipping mirroring...")
	}

	// 2. マージリクエストの移行（リクエストされている場合）
	if cfg.IncludePRs {
		logger.Info("Migrating merge requests...")
		
		// マイグレーションオプションを設定
		migrationOpts := &migration.MigrationOptions{
			ContinueFromID: cfg.ContinueFromMRID,
			DryRun:         false,
		}
		
		if err := migration.MigrateMergeRequests(ctx, gitlabClient, githubClient, cfg, migrationOpts); err != nil {
			return fmt.Errorf("failed to migrate merge requests: %w", err)
		}
	}

	logger.Info("Migration completed successfully!")
	return nil
}