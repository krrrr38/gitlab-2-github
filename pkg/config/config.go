package config

type Config struct {
	GitLabToken       string
	GitLabURL         string
	GitLabProjectID   string
	GitHubToken       string
	GitHubOwner       string
	GitHubRepo        string
	TempDir           string
	IncludePRs        bool
	FilterMergeReqIDs []int
	ContinueFromMRID  int // 指定したMR IDから処理を再開
	LogLevel          string
	ForceRecreate     bool // GitHubリポジトリを強制的に削除して再作成する
}
