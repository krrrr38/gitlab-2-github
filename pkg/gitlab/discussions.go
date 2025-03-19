package gitlab

import (
	"github.com/xanzy/go-gitlab"
)

// DiscussionNote represents a note within a discussion with parent-child relationships
type DiscussionNote struct {
	Note       *gitlab.Note
	ParentID   int    // GitLab note ID of the parent (0 if it's a root note)
	Discussion string // Discussion ID this note belongs to
}

// GetMergeRequestDiscussions retrieves discussions from a GitLab merge request
func GetMergeRequestDiscussions(client *gitlab.Client, projectID string, mrIID int) ([]*gitlab.Discussion, error) {
	// Get all discussions for the MR
	discussions, _, err := client.Discussions.ListMergeRequestDiscussions(projectID, mrIID, &gitlab.ListMergeRequestDiscussionsOptions{})
	if err != nil {
		return nil, err
	}
	return discussions, nil
}
