package sync

import "strings"

// IsSyncableRemotePageStatus reports whether a remote page lifecycle status
// should be represented as a local markdown file.
func IsSyncableRemotePageStatus(status string) bool {
	normalized := strings.TrimSpace(strings.ToLower(status))
	switch normalized {
	case "", "current", "draft":
		return true
	default:
		return false
	}
}
