package gitlab

import (
	"github.com/krrrr38/gitlab-2-github/pkg/logger"
	"github.com/xanzy/go-gitlab"
)

// DiscussionNote represents a note within a discussion with parent-child relationships
type DiscussionNote struct {
	Note       *gitlab.Note
	ParentID   int    // GitLab note ID of the parent (0 if it's a root note)
	Discussion string // Discussion ID this note belongs to
}

// GetMergeRequestDiscussions retrieves discussions from a GitLab merge request
// and organizes them into a structured format that tracks parent-child relationships
func GetMergeRequestDiscussions(client *gitlab.Client, projectID string, mrIID int) (map[int]*DiscussionNote, error) {
	// Get all discussions for the MR
	discussions, _, err := client.Discussions.ListMergeRequestDiscussions(projectID, mrIID, &gitlab.ListMergeRequestDiscussionsOptions{})
	if err != nil {
		return nil, err
	}
	
	// Map to store notes with their relationships
	noteMap := make(map[int]*DiscussionNote)
	
	// Process all discussions and their notes
	for _, discussion := range discussions {
		// Skip system-generated discussions
		if len(discussion.Notes) == 0 || discussion.Notes[0].System {
			continue
		}
		
		// First note in the discussion is the root note
		firstNote := discussion.Notes[0]
		noteMap[firstNote.ID] = &DiscussionNote{
			Note:       firstNote,
			ParentID:   0, // Root note has no parent
			Discussion: discussion.ID,
		}
		
		// Process replies (if any)
		for i := 1; i < len(discussion.Notes); i++ {
			note := discussion.Notes[i]
			
			// All subsequent notes in a discussion are replies to the first note
			noteMap[note.ID] = &DiscussionNote{
				Note:       note,
				ParentID:   firstNote.ID,
				Discussion: discussion.ID,
			}
		}
	}
	
	logger.Info("Found discussions for MR", "count", len(discussions), "notes", len(noteMap))
	return noteMap, nil
}