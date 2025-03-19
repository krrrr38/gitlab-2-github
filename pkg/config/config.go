package config

type GlobalConfig struct {
	GitLabToken   string
	GitLabURL     string
	GitLabProject string
	GitHubToken   string
	GitHubOwner   string
	GitHubRepo    string
	WorkingDir    string
	LogLevel      string
}

type MigrateConfig struct {
	FilterMergeReqIDs []int
	ContinueFromMRID  int // 指定したMR IDから処理を再開
}
