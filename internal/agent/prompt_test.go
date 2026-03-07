package agent

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBuildSystemPrompt(t *testing.T) {
	tests := []struct {
		name       string
		workDir    string
		wantSubstr string
	}{
		{
			name:       "contains working directory",
			workDir:    "/home/user/project",
			wantSubstr: "/home/user/project",
		},
		{
			name:       "contains platform",
			workDir:    "/tmp",
			wantSubstr: runtime.GOOS,
		},
		{
			name:       "contains today's date",
			workDir:    "/tmp",
			wantSubstr: time.Now().Format("2006-01-02"),
		},
		{
			name:       "contains agent identity",
			workDir:    "/tmp",
			wantSubstr: "coding agent",
		},
		{
			name:       "mentions tools",
			workDir:    "/tmp",
			wantSubstr: "tools",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildSystemPrompt(tt.workDir)
			if !strings.Contains(got, tt.wantSubstr) {
				t.Errorf("BuildSystemPrompt(%q) does not contain %q\ngot: %s", tt.workDir, tt.wantSubstr, got)
			}
		})
	}
}
