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

	configUserNameCmd := fmt.Sprintf("cd %s && git config --local user.name \"%s\"", g.workingDir, "gitlab-2-github")
	if err := utils.ExecuteCommand(configUserNameCmd); err != nil {
		return fmt.Errorf("failed to set git config user.name: %w", err)
	}
	configUserEmailCmd := fmt.Sprintf("cd %s && git config --local user.email \"%s\"", g.workingDir, "gitlab-2-github@example.com")
	if err := utils.ExecuteCommand(configUserEmailCmd); err != nil {
		return fmt.Errorf("failed to set git config user.name: %w", err)
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
	pullCmd := fmt.Sprintf("cd %s && git pull gitlab HEAD", g.workingDir)
	if err := utils.ExecuteCommand(pullCmd); err != nil {
		return fmt.Errorf("failed to pull from GitLab: %w", err)
	}

	// Push everything to GitHub
	// tagやbranchの件数が多い状態でまとめてpushをすると、GitHubで500が返却されることがあるため、分割してpushする
	pushTagsCmd := fmt.Sprintf("cd %s && git push origin --tags", g.workingDir)
	if err := utils.ExecuteCommand(pushTagsCmd); err != nil {
		return fmt.Errorf("failed to push tags to GitHub: %w", err)
	}
	pushAllCmd := fmt.Sprintf("cd %s && git push origin --all", g.workingDir)
	if err := utils.ExecuteCommand(pushAllCmd); err != nil {
		return fmt.Errorf("failed to push all to GitHub: %w", err)
	}
	return nil
}

func (g *Git) CreateBranch(branch, sha string) error {
	// 削除済みのMRにおけるcommitなどは手元にないため、その場合には、shaを指定してfetchする
	catFile, _ := utils.ExecuteCommandOutput(fmt.Sprintf("cd %s && git cat-file -t %s", g.workingDir, sha))
	if !strings.Contains(catFile, "commit") {
		fetchShaCmd := fmt.Sprintf("cd %s && git fetch gitlab %s", g.workingDir, sha)
		if err := utils.ExecuteCommand(fetchShaCmd); err != nil {
			return fmt.Errorf("failed to fetch sha from GitLab: %w", err)
		}
	}

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

func (g *Git) Commit(comment string, options ...string) error {
	commitCmd := fmt.Sprintf("cd %s && git commit %s -m '%s'",
		g.workingDir, strings.Join(options, " "), comment)
	if err := utils.ExecuteCommand(commitCmd); err != nil {
		return fmt.Errorf("failed to commit changes: %w", err)
	}
	return nil
}

func (g *Git) PushBranchOrigins(branches ...string) error {
	pushSourceCmd := fmt.Sprintf("cd %s && git push origin %s --force", g.workingDir, strings.Join(branches, " "))
	if err := utils.ExecuteCommand(pushSourceCmd); err != nil {
		return fmt.Errorf("failed to push source branch: %w", err)
	}
	return nil
}
