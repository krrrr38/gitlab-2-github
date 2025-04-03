package utils

import (
	"fmt"
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
	"os"
	"os/exec"
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

// ExecuteCommandoutput executes a shell command
func ExecuteCommandOutput(cmd string) (string, error) {
	logger.Debug("Executing command with output", "cmd", cmd)

	c := exec.Command("bash", "-c", cmd)
	output, err := c.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("command failed: %s\nOutput: %s", err, output)
	}
	return string(output), nil
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
