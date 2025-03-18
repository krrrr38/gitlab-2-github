package git

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/krrrr38/gitlab-2-github/pkg/logger"
)

// ExecuteCommand executes a shell command
func ExecuteCommand(cmd string) error {
	logger.Debug("Executing command", "cmd", cmd)

	c := exec.Command("bash", "-c", cmd)
	output, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("command failed: %s\nOutput: %s", err, output)
	}
	return nil
}

// ExecuteCommandWithOutput executes a shell command and returns the output
func ExecuteCommandWithOutput(cmd string) (string, error) {
	logger.Debug("Executing command with output", "cmd", cmd)

	c := exec.Command("bash", "-c", cmd)
	output, err := c.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("command failed: %s\nOutput: %s", err, output)
	}
	return string(output), nil
}

// CreateDirectory creates a directory if it doesn't exist
func CreateDirectory(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	return nil
}

// CleanupDirectory removes and recreates a directory
func CleanupDirectory(dir string) error {
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to clean up directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	return nil
}

// IsMergeCommit checks if a commit is a merge commit
func IsMergeCommit(repoDir, commitSHA string) (bool, error) {
	cmd := fmt.Sprintf("cd %s && git cat-file -p %s | grep -i ^parent | wc -l", repoDir, commitSHA)
	output, err := ExecuteCommandWithOutput(cmd)
	if err != nil {
		return false, fmt.Errorf("failed to check if commit is a merge commit: %w", err)
	}

	// Trim output and convert to int
	output = strings.TrimSpace(output)
	parentCount, err := strconv.Atoi(output)
	if err != nil {
		return false, fmt.Errorf("failed to parse parent count: %w", err)
	}

	// If there are 2 or more parents, it's a merge commit
	return parentCount >= 2, nil
}

// CherryPickCommit cherry-picks a commit with special handling for merge commits
func CherryPickCommit(repoDir, commitSHA string, allowEmpty bool, keepRedundant bool) error {
	// Check if this is a merge commit
	isMerge, err := IsMergeCommit(repoDir, commitSHA)
	if err != nil {
		return err
	}

	// Build options
	var options string
	if allowEmpty {
		options += " --allow-empty"
	}
	if keepRedundant {
		options += " --keep-redundant-commits"
	}

	var cherryPickCmd string
	if isMerge {
		// For merge commits, use -m 1 to select the first parent as mainline
		cherryPickCmd = fmt.Sprintf("cd %s && git cherry-pick%s -m 1 %s", repoDir, options, commitSHA)
	} else {
		// For regular commits, use normal cherry-pick
		cherryPickCmd = fmt.Sprintf("cd %s && git cherry-pick%s %s", repoDir, options, commitSHA)
	}

	logger.Debug("Cherry-picking commit", "sha", commitSHA, "is_merge", isMerge)
	return ExecuteCommand(cherryPickCmd)
}
