package utils

import (
	"fmt"
	"unicode/utf8"
)

const (
	// GitHubの各種テキスト長制限
	// https://docs.github.com/en/rest/pulls/pulls?apiVersion=2022-11-28
	MaxPRTitleLength       = 256   // Pull Requestのタイトル最大長
	MaxPRDescriptionLength = 65536 // Pull Requestの説明文最大長（64KB）
	MaxCommentLength       = 65536 // コメントの最大長（64KB）

	// 切り詰め表示用のサフィックス
	TruncateSuffix = "... [truncated]"
)

// TruncateText は指定された最大長に基づいてテキストを切り詰めます
func TruncateText(text string, maxLength int) string {
	if utf8.RuneCountInString(text) <= maxLength {
		return text
	}

	// 最大長からサフィックス長を引いた長さまで切り詰める
	availableLength := maxLength - utf8.RuneCountInString(TruncateSuffix)
	if availableLength <= 0 {
		// 極端に短い場合は単にmaxLengthまで切る
		runes := []rune(text)
		return string(runes[:maxLength])
	}

	runes := []rune(text)
	return string(runes[:availableLength]) + TruncateSuffix
}

// WrapComment はコメントを適切にラップします
func WrapComment(summary, detail string) string {
	// GitHubではコメントを折りたたむための専用Markdownフォーマット
	return fmt.Sprintf("<details>\n<summary>%s</summary>\n\n%s\n</details>",
		summary, detail)
}

// WrapComment はコメントを適切にラップします
func WrapCommentAsResolved(detail string) string {
	// GitHubではコメントを折りたたむための専用Markdownフォーマット
	return fmt.Sprintf("<details><summary>Resolved</summary>\n\n%s\n</details>",
		TruncateText(detail, MaxCommentLength-35))
}
