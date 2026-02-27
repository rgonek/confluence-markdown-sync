package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestEnsureSynchronizedCmdOutput(t *testing.T) {
	runParallelCommandTest(t)
	cmd := newCleanCmd()
	cmd.SetOut(os.Stdout) // Default behavior mapping

	out := ensureSynchronizedCmdOutput(cmd)
	if out == nil {
		t.Fatalf("expected output writer, got nil")
	}

	// Try overriding it
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)

	out = ensureSynchronizedCmdOutput(cmd)
	if out != buf {
		// Output sync might wrap it, just ensure it doesn't panic
		t.Logf("output wrapped: %T", out)
	}
}

func TestRequireSafetyConfirmation(t *testing.T) {
	runParallelCommandTest(t)

	// Backup flags
	oldYes := flagYes
	oldNonInteractive := flagNonInteractive
	defer func() {
		flagYes = oldYes
		flagNonInteractive = oldNonInteractive
	}()

	tests := []struct {
		name         string
		yes          bool
		nonInt       bool
		changedCount int
		hasDeletes   bool
		input        string
		wantErr      bool
		errMatch     string
	}{
		{
			name:         "below threshold no deletes",
			changedCount: 5,
			hasDeletes:   false,
			wantErr:      false,
		},
		{
			name:         "at threshold no deletes",
			changedCount: 10,
			hasDeletes:   false,
			wantErr:      false,
		},
		{
			name:         "above threshold non-interactive fails",
			nonInt:       true,
			changedCount: 11,
			wantErr:      true,
			errMatch:     "requires confirmation (11 files)",
		},
		{
			name:         "deletes non-interactive fails",
			nonInt:       true,
			changedCount: 1,
			hasDeletes:   true,
			wantErr:      true,
			errMatch:     "requires confirmation (delete operations",
		},
		{
			name:         "above threshold with --yes passes",
			yes:          true,
			changedCount: 11,
			wantErr:      false,
		},
		{
			name:         "interactive accept",
			changedCount: 11,
			input:        "y\n",
			wantErr:      false,
		},
		{
			name:         "interactive decline",
			changedCount: 11,
			input:        "n\n",
			wantErr:      true,
			errMatch:     "cancelled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagYes = tt.yes
			flagNonInteractive = tt.nonInt

			in := strings.NewReader(tt.input)
			out := new(bytes.Buffer)

			err := requireSafetyConfirmation(in, out, "TestAction", tt.changedCount, tt.hasDeletes)
			if (err != nil) != tt.wantErr {
				t.Errorf("requireSafetyConfirmation() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMatch != "" && !strings.Contains(err.Error(), tt.errMatch) {
				t.Errorf("error %v does not match %q", err, tt.errMatch)
			}
		})
	}
}

func TestResolvePushConflictPolicy(t *testing.T) {
	runParallelCommandTest(t)

	oldNonInteractive := flagNonInteractive
	defer func() {
		flagNonInteractive = oldNonInteractive
	}()

	tests := []struct {
		name       string
		nonInt     bool
		onConflict string
		isSpace    bool
		input      string
		want       string
		wantErr    bool
	}{
		{
			name:       "flag override",
			onConflict: "force",
			want:       "force",
		},
		{
			name:    "space default",
			isSpace: true,
			want:    OnConflictPullMerge,
		},
		{
			name:    "non-interactive missing flag",
			nonInt:  true,
			wantErr: true,
		},
		{
			name:  "interactive select pull-merge",
			input: "pull-merge\n",
			want:  OnConflictPullMerge,
		},
		{
			name:  "interactive select force",
			input: "f\n",
			want:  OnConflictForce,
		},
		{
			name:  "interactive select cancel default",
			input: "\n",
			want:  OnConflictCancel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagNonInteractive = tt.nonInt

			in := strings.NewReader(tt.input)
			out := new(bytes.Buffer)

			got, err := resolvePushConflictPolicy(in, out, tt.onConflict, tt.isSpace)
			if (err != nil) != tt.wantErr {
				t.Errorf("resolvePushConflictPolicy() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("resolvePushConflictPolicy() = %v, want %v", got, tt.want)
			}
		})
	}
}
