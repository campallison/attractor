package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

)

func TestExecuteReadFile(t *testing.T) {
	dir := t.TempDir()

	// Create a 5-line test file.
	content := "line one\nline two\nline three\nline four\nline five\n"
	writeTestFile(t, dir, "test.txt", content)

	// Create a binary file.
	writeTestBinary(t, dir, "binary.dat", []byte{0x00, 0x01, 0x02, 0xFF})

	// Create an empty file.
	writeTestFile(t, dir, "empty.txt", "")

	tests := []struct {
		name       string
		args       readFileArgs
		wantSubstr string
		wantErr    bool
	}{
		{
			name:       "read full file with line numbers",
			args:       readFileArgs{FilePath: "test.txt"},
			wantSubstr: "1 | line one",
		},
		{
			name:       "last line has line number",
			args:       readFileArgs{FilePath: "test.txt"},
			wantSubstr: "5 | line five",
		},
		{
			name:       "offset and limit",
			args:       readFileArgs{FilePath: "test.txt", Offset: 3, Limit: 2},
			wantSubstr: "3 | line three",
		},
		{
			name:       "offset beyond file length",
			args:       readFileArgs{FilePath: "test.txt", Offset: 100},
			wantSubstr: "beyond end of file",
		},
		{
			name:    "file not found",
			args:    readFileArgs{FilePath: "nonexistent.txt"},
			wantErr: true,
		},
		{
			name:    "binary file detected",
			args:    readFileArgs{FilePath: "binary.dat"},
			wantErr: true,
		},
		{
			name:       "empty file",
			args:       readFileArgs{FilePath: "empty.txt"},
			wantSubstr: "empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rawArgs, _ := json.Marshal(tt.args)
			got, err := executeReadFile(rawArgs, dir)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(got, tt.wantSubstr) {
				t.Errorf("output %q does not contain %q", got, tt.wantSubstr)
			}
		})
	}
}

func TestResolvePath(t *testing.T) {
	workDir := "/home/user/project"

	tests := []struct {
		name    string
		path    string
		want    string
		wantErr string
	}{
		{
			name: "simple relative path",
			path: "src/main.go",
			want: "/home/user/project/src/main.go",
		},
		{
			name: "current directory",
			path: ".",
			want: "/home/user/project",
		},
		{
			name: "nested subdirectory",
			path: "a/b/c/file.txt",
			want: "/home/user/project/a/b/c/file.txt",
		},
		{
			name:    "absolute path rejected",
			path:    "/etc/passwd",
			wantErr: "absolute paths are not allowed",
		},
		{
			name:    "dot-dot escape rejected",
			path:    "../secret.txt",
			wantErr: "path escapes working directory",
		},
		{
			name:    "deep dot-dot escape rejected",
			path:    "../../.ssh/id_rsa",
			wantErr: "path escapes working directory",
		},
		{
			name:    "sneaky dot-dot inside path rejected",
			path:    "src/../../etc/passwd",
			wantErr: "path escapes working directory",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolvePath(tt.path, workDir)
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
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExecuteReadFile_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "safe.txt", "safe content\n")

	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{
			name:    "absolute path rejected",
			path:    "/etc/passwd",
			wantErr: "absolute paths are not allowed",
		},
		{
			name:    "dot-dot escape rejected",
			path:    "../../etc/passwd",
			wantErr: "path escapes working directory",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rawArgs, _ := json.Marshal(readFileArgs{FilePath: tt.path})
			_, err := executeReadFile(rawArgs, dir)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestExecuteReadFileOffsetLimit(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	writeTestFile(t, dir, "ten.txt", strings.Join(lines, "\n")+"\n")

	rawArgs, _ := json.Marshal(readFileArgs{FilePath: "ten.txt", Offset: 3, Limit: 2})
	got, err := executeReadFile(rawArgs, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain lines 3 and 4 but not lines 2 or 5.
	want := []string{"3 | line 3", "4 | line 4"}
	notWant := []string{"2 | line 2", "5 | line 5"}

	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("output missing expected %q", w)
		}
	}
	for _, nw := range notWant {
		if strings.Contains(got, nw) {
			t.Errorf("output unexpectedly contains %q", nw)
		}
	}
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test file %s: %v", name, err)
	}
}

func writeTestBinary(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatalf("failed to write test binary %s: %v", name, err)
	}
}
