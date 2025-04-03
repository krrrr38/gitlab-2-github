package migration

// MigrationOptions はマイグレーションのオプション設定を含む構造体
type MigrationOptions struct {
	// 特定のMR IDから再開する場合に指定
	ContinueFromID int
	// 特定のMR IDのみを対象とする場合に指定
	FilterMergeReqIDs []int
	// 1つのMRに対するディスカッションの移行数の上限
	MaxDiscussions int
}
