package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestExecuteEditFile(t *testing.T) {
	tests := []struct {
		name        string
		initial     string
		args        editFileArgs
		wantContent string
		wantSubstr  string
		wantErr     string
	}{
		{
			name:    "single replacement",
			initial: "hello world",
			args: editFileArgs{
				FilePath:  "test.txt",
				OldString: "world",
				NewString: "Go",
			},
			wantContent: "hello Go",
			wantSubstr:  "Replaced 1 occurrence",
		},
		{
			name:    "not found",
			initial: "hello world",
			args: editFileArgs{
				FilePath:  "test.txt",
				OldString: "missing",
				NewString: "replaced",
			},
			wantErr: "not found",
		},
		{
			name:    "not unique without replace_all",
			initial: "aaa bbb aaa ccc aaa",
			args: editFileArgs{
				FilePath:  "test.txt",
				OldString: "aaa",
				NewString: "xxx",
			},
			wantErr: "not unique",
		},
		{
			name:    "replace all",
			initial: "aaa bbb aaa ccc aaa",
			args: editFileArgs{
				FilePath:   "test.txt",
				OldString:  "aaa",
				NewString:  "xxx",
				ReplaceAll: true,
			},
			wantContent: "xxx bbb xxx ccc xxx",
			wantSubstr:  "Replaced 3 occurrences",
		},
		{
			name: "file not found",
			args: editFileArgs{
				FilePath:  "nonexistent.txt",
				OldString: "a",
				NewString: "b",
			},
			wantErr: "no such file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()

			if tt.initial != "" {
				writeTestFile(t, dir, "test.txt", tt.initial)
			}

			rawArgs, _ := json.Marshal(tt.args)
			got, err := executeEditFile(rawArgs, dir)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !strings.Contains(got, tt.wantSubstr) {
				t.Errorf("output %q does not contain %q", got, tt.wantSubstr)
			}

			data, err := os.ReadFile(filepath.Join(dir, "test.txt"))
			if err != nil {
				t.Fatalf("failed to read file: %v", err)
			}
			if diff := cmp.Diff(tt.wantContent, string(data)); diff != "" {
				t.Errorf("file content mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
