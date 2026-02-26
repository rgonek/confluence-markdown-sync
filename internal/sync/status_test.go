package sync

import "testing"

func TestIsSyncableRemotePageStatus(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   bool
	}{
		{name: "empty defaults current", status: "", want: true},
		{name: "current", status: "current", want: true},
		{name: "draft", status: "draft", want: true},
		{name: "mixed case current", status: "Current", want: true},
		{name: "trashed", status: "trashed", want: false},
		{name: "archived", status: "archived", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSyncableRemotePageStatus(tt.status); got != tt.want {
				t.Fatalf("IsSyncableRemotePageStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}
