package gitlab

import (
	"fmt"

	"github.com/krrrr38/gitlab-2-github/pkg/logger"
	"github.com/xanzy/go-gitlab"
)

// GetMergeRequests retrieves merge requests from GitLab project
func GetMergeRequests(client *gitlab.Client, projectID string, filterIDs []int) ([]*gitlab.MergeRequest, error) {
	// List all merge requests from GitLab
	opts := &gitlab.ListProjectMergeRequestsOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: 100,
		},
	}

	var allMRs []*gitlab.MergeRequest
	for {
		mrs, resp, err := client.MergeRequests.ListProjectMergeRequests(projectID, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list GitLab merge requests: %w", err)
		}
		
		allMRs = append(allMRs, mrs...)
		
		if resp.NextPage == 0 {
			break
		}
		
		opts.Page = resp.NextPage
	}
	
	logger.Info("Found merge requests to migrate", "count", len(allMRs))
	
	// Filter merge requests if specific IDs were provided
	if len(filterIDs) > 0 {
		filteredMRs := make([]*gitlab.MergeRequest, 0)
		for _, mr := range allMRs {
			for _, id := range filterIDs {
				if mr.IID == id {
					filteredMRs = append(filteredMRs, mr)
					break
				}
			}
		}
		allMRs = filteredMRs
		logger.Info("Filtered merge requests", "count", len(allMRs))
	}

	return allMRs, nil
}

// GetMergeRequestNotes retrieves comments from a GitLab merge request
func GetMergeRequestNotes(client *gitlab.Client, projectID string, mrIID int) ([]*gitlab.Note, error) {
	noteOpts := &gitlab.ListMergeRequestNotesOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: 100,
		},
	}
	
	var allNotes []*gitlab.Note
	for {
		notes, resp, err := client.Notes.ListMergeRequestNotes(projectID, mrIID, noteOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to list GitLab MR notes: %w", err)
		}
		
		allNotes = append(allNotes, notes...)
		
		if resp.NextPage == 0 {
			break
		}
		
		noteOpts.Page = resp.NextPage
	}
	
	logger.Info("Found comments to migrate", "count", len(allNotes), "mr_id", mrIID)
	
	return allNotes, nil
}