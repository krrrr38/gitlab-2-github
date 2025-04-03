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
func GetMergeRequestDiscussions(client *gitlab.Client, projectID string, mrIID, maxDiscussions int) ([]*gitlab.Discussion, error) {
	// Get all discussions for the MR
	var ret []*gitlab.Discussion
	var page = 1
	for {
		discussions, _, err := client.Discussions.ListMergeRequestDiscussions(projectID, mrIID, &gitlab.ListMergeRequestDiscussionsOptions{
			PerPage: 100,
			Page:    page,
		})
		if err != nil {
			return nil, err
		}
		ret = append(ret, discussions...)
		if len(discussions) < 100 {
			break
		}
		if maxDiscussions > 0 && len(ret) >= maxDiscussions {
			break
		}
		page += 1
	}
	return ret, nil
}
