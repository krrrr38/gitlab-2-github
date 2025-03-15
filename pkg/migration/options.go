package migration

// MigrationOptions はマイグレーションのオプション設定を含む構造体
type MigrationOptions struct {
	// 特定のMR IDから再開する場合に指定
	ContinueFromID int
	
	// ドライラン（実際には変更を行わない）
	DryRun         bool
}