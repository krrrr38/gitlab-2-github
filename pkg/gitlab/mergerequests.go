package gitlab

import (
	"fmt"
	"time"

	"github.com/krrrr38/gitlab-2-github/pkg/logger"
	"github.com/xanzy/go-gitlab"
)

// ApprovalInfo はマージリクエストの承認情報を格納する構造体
type ApprovalInfo struct {
	User      string    // 承認者のユーザー名
	CreatedAt time.Time // 承認日時
}

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

// GetMergeRequestApprovals retrieves approval information for a GitLab merge request
func GetMergeRequestApprovals(client *gitlab.Client, projectID string, mrIID int) ([]ApprovalInfo, error) {
	// マージリクエストの承認情報を取得
	_, _, err := client.MergeRequestApprovals.GetConfiguration(projectID, mrIID)
	if err != nil {
		return nil, fmt.Errorf("failed to get MR approval configuration: %w", err)
	}

	// 承認履歴を取得
	approvalState, _, err := client.MergeRequestApprovals.GetApprovalState(projectID, mrIID)
	if err != nil {
		return nil, fmt.Errorf("failed to get MR approval state: %w", err)
	}

	// 承認情報を整理
	var approvalInfos []ApprovalInfo

	// 承認者情報を収集
	for _, approval := range approvalState.Rules {
		for _, approver := range approval.ApprovedBy {
			if approver == nil || approver.Username == "" {
				continue
			}

			// 承認日時はAPIから直接取得できないため、
			// 承認に関連するコメントやイベントから推測する必要がある
			approvalInfos = append(approvalInfos, ApprovalInfo{
				User:      approver.Username,
				CreatedAt: time.Now(), // 現時点では正確な承認日時は取得できないため現在時刻を設定
			})
		}
	}

	// 承認日時を取得するために、マージリクエストのイベントを確認
	events, err := GetMergeRequestEvents(client, projectID, mrIID)
	if err != nil {
		logger.Warn("Failed to get MR events for approval timestamps", "error", err)
		// エラーがあっても処理は続行
	} else {
		// イベントから承認日時を更新
		updateApprovalTimesFromEvents(events, &approvalInfos)
	}

	logger.Info("Found approvals for MR", "count", len(approvalInfos), "mr_id", mrIID)
	return approvalInfos, nil
}

// GetMergeRequestCommits retrieves commits from a GitLab merge request
func GetMergeRequestCommits(client *gitlab.Client, projectID string, mrIID int) ([]*gitlab.Commit, error) {
	opts := &gitlab.GetMergeRequestCommitsOptions{
		PerPage: 100,
	}

	var allCommits []*gitlab.Commit
	for {
		commits, resp, err := client.MergeRequests.GetMergeRequestCommits(projectID, mrIID, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list GitLab MR commits: %w", err)
		}

		allCommits = append(allCommits, commits...)

		if resp.NextPage == 0 {
			break
		}

		opts.Page = resp.NextPage
	}

	logger.Info("Found commits for MR", "count", len(allCommits), "mr_id", mrIID)
	return allCommits, nil
}

// GetMergeRequestEvents retrieves events for a GitLab merge request
func GetMergeRequestEvents(client *gitlab.Client, projectID string, mrIID int) ([]*gitlab.StateEvent, error) {
	opts := &gitlab.ListStateEventsOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: 100,
		},
	}

	var allEvents []*gitlab.StateEvent
	for {
		events, resp, err := client.ResourceStateEvents.ListMergeStateEvents(projectID, mrIID, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list GitLab MR events: %w", err)
		}

		allEvents = append(allEvents, events...)

		if resp.NextPage == 0 {
			break
		}

		opts.Page = resp.NextPage
	}

	return allEvents, nil
}

// updateApprovalTimesFromEvents updates approval times based on resource state events
func updateApprovalTimesFromEvents(events []*gitlab.StateEvent, approvals *[]ApprovalInfo) {
	// イベントから承認に関連するものを探す
	approvalEvents := make(map[string]time.Time)

	for _, event := range events {
		if event.State == "approved" && event.User != nil {
			approvalEvents[event.User.Username] = *event.CreatedAt
		}
	}

	// 承認情報の日時を更新
	for i, approval := range *approvals {
		if timestamp, ok := approvalEvents[approval.User]; ok {
			(*approvals)[i].CreatedAt = timestamp
		}
	}
}
