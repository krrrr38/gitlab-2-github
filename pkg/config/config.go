package config

type GlobalConfig struct {
	GitLabToken               string
	GitLabURL                 string
	GitLabProject             string
	GitHubGitToken            string
	GitHubApiToken            string
	GitHubAppID               int
	GitHubAppInstallationID   int
	GitHubAppPrivateKey       string
	GitHubAppPrivateKeyAsFile bool
	GitHubOwner               string
	GitHubRepo                string
	WorkingDir                string
	LogLevel                  string
}

type MigrateConfig struct {
	FilterMergeReqIDs []int
	ContinueFromMRID  int // 指定したMR IDから処理を再開
	MaxDiscussions    int // ディスカッションの移行数の上限（未指定の場合はすべて）
}
