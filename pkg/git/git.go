package git

import (
	"fmt"
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
	"github.com/krrrr38/gitlab-2-github/pkg/utils"
	"strings"
)

type Git struct {
	workingDir    string
	githubOwner   string
	githubRepo    string
	gitlabURL     string
	gitlabProject string
}

func NewGit(workingDir, githubOwner, githubRepo, gitlabURL, gitlabProject string) *Git {
	return &Git{
		workingDir:    workingDir,
		githubOwner:   githubOwner,
		githubRepo:    githubRepo,
		gitlabURL:     gitlabURL,
		gitlabProject: gitlabProject,
	}
}

func (g *Git) Init(githubToken, gitlabToken string) error {
	_ = utils.CleanupDirectory(g.workingDir)

	// Clone the repository
	repoURL := fmt.Sprintf("https://%s@github.com/%s/%s.git",
		githubToken,
		g.githubOwner,
		g.githubRepo)
	cloneCmd := fmt.Sprintf("git clone %s %s", repoURL, g.workingDir)
	if err := utils.ExecuteCommand(cloneCmd); err != nil {
		return fmt.Errorf("failed to clone GitHub repository: %w", err)
	}

	// Add GitLab remote to help with Git operations
	gitlabRemoteURL := fmt.Sprintf("https://oauth2:%s@%s/%s.git",
		gitlabToken,
		strings.TrimPrefix(g.gitlabURL, "https://"),
		g.gitlabProject)
	addRemoteCmd := fmt.Sprintf("cd %s && git remote add gitlab %s", g.workingDir, gitlabRemoteURL)
	if err := utils.ExecuteCommand(addRemoteCmd); err != nil {
		return fmt.Errorf("failed to add GitLab remote: %w", err)
	}

	// Fetch everything from GitLab
	fetchCmd := fmt.Sprintf("cd %s && git fetch gitlab --prune --tags", g.workingDir)
	if err := utils.ExecuteCommand(fetchCmd); err != nil {
		return fmt.Errorf("failed to fetch from GitLab: %w", err)
	}

	// Push everything to GitHub
	pushCmd := fmt.Sprintf("cd %s && git push origin --all", g.workingDir)
	if err := utils.ExecuteCommand(pushCmd); err != nil {
		return fmt.Errorf("failed to push to GitHub: %w", err)
	}
	pushTagsCmd := fmt.Sprintf("cd %s && git push origin --tags", g.workingDir)
	if err := utils.ExecuteCommand(pushTagsCmd); err != nil {
		return fmt.Errorf("failed to push tags to GitHub: %w", err)
	}
	return nil
}

func (g *Git) CreateBranch(branch, sha string) error {
	// Create branch from base_sha
	baseSHACmd := fmt.Sprintf("cd %s && git checkout -b %s %s",
		g.workingDir, branch, sha)
	if err := utils.ExecuteCommand(baseSHACmd); err != nil {
		logger.Warn("Failed to checkout branch from sha",
			"branch", branch,
			"sha", sha,
			"error", err)

		// Fallback to using target branch directly
		branchCmd := fmt.Sprintf("cd %s && git checkout -b %s gitlab/%s",
			g.workingDir, branch, branch)

		if err := utils.ExecuteCommand(branchCmd); err != nil {
			return fmt.Errorf("failed to create branch: %w", err)
		}
	}
	return nil
}

func (g *Git) PushBranchOrigin(branch string) error {
	pushSourceCmd := fmt.Sprintf("cd %s && git push origin %s --force", g.workingDir, branch)
	if err := utils.ExecuteCommand(pushSourceCmd); err != nil {
		return fmt.Errorf("failed to push source branch: %w", err)
	}
	return nil
}
