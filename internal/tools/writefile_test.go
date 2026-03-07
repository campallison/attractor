package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestExecuteWriteFile(t *testing.T) {
	tests := []struct {
		name        string
		filePath    string
		content     string
		preExisting string
		wantContent string
		wantSubstr  string
	}{
		{
			name:        "write new file",
			filePath:    "new.txt",
			content:     "hello world",
			wantContent: "hello world",
			wantSubstr:  "11 bytes",
		},
		{
			name:        "overwrite existing file",
			filePath:    "existing.txt",
			content:     "new content",
			preExisting: "old content",
			wantContent: "new content",
			wantSubstr:  "11 bytes",
		},
		{
			name:        "nested directory creation",
			filePath:    "a/b/c/deep.txt",
			content:     "deep file",
			wantContent: "deep file",
			wantSubstr:  "9 bytes",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()

			if tt.preExisting != "" {
				writeTestFile(t, dir, tt.filePath, tt.preExisting)
			}

			rawArgs, _ := json.Marshal(writeFileArgs{
				FilePath: tt.filePath,
				Content:  tt.content,
			})
			got, err := executeWriteFile(rawArgs, dir)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !strings.Contains(got, tt.wantSubstr) {
				t.Errorf("output %q does not contain %q", got, tt.wantSubstr)
			}

			data, err := os.ReadFile(filepath.Join(dir, tt.filePath))
			if err != nil {
				t.Fatalf("failed to read written file: %v", err)
			}
			if diff := cmp.Diff(tt.wantContent, string(data)); diff != "" {
				t.Errorf("file content mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
