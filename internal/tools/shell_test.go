package tools

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFilterEnvVars(t *testing.T) {
	tests := []struct {
		name   string
		input  []string
		want   []string
	}{
		{
			name: "strips API keys",
			input: []string{
				"PATH=/usr/bin",
				"OPENROUTER_API_KEY=sk-secret",
				"HOME=/home/user",
			},
			want: []string{
				"PATH=/usr/bin",
				"HOME=/home/user",
			},
		},
		{
			name: "strips secrets and tokens",
			input: []string{
				"DB_SECRET=hunter2",
				"AUTH_TOKEN=tok123",
				"GOPATH=/go",
			},
			want: []string{
				"GOPATH=/go",
			},
		},
		{
			name: "strips passwords and credentials",
			input: []string{
				"ADMIN_PASSWORD=pass",
				"AWS_CREDENTIAL=cred",
				"LANG=en_US.UTF-8",
			},
			want: []string{
				"LANG=en_US.UTF-8",
			},
		},
		{
			name: "case insensitive matching",
			input: []string{
				"my_api_key=lower",
				"MY_SECRET=upper",
				"User_Token=mixed",
				"SHELL=/bin/zsh",
			},
			want: []string{
				"SHELL=/bin/zsh",
			},
		},
		{
			name: "keeps all non-sensitive vars",
			input: []string{
				"PATH=/usr/bin",
				"HOME=/home/user",
				"GOPATH=/go",
				"LANG=en_US.UTF-8",
				"TERM=xterm-256color",
			},
			want: []string{
				"PATH=/usr/bin",
				"HOME=/home/user",
				"GOPATH=/go",
				"LANG=en_US.UTF-8",
				"TERM=xterm-256color",
			},
		},
		{
			name: "strips broader sensitive suffixes",
			input: []string{
				"PRIVATE_KEY=pk",
				"GOOGLE_APPLICATION_CREDENTIALS=/path/to/creds.json",
				"MYSQL_PASSWD=secret",
				"BASIC_AUTH=user:pass",
				"GITHUB_PRIVATE=yes",
				"ENCRYPTION_KEY=aes256",
				"EDITOR=vim",
			},
			want: []string{
				"EDITOR=vim",
			},
		},
		{
			name:  "empty input",
			input: []string{},
			want:  []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterEnvVars(tt.input)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("filterEnvVars() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIsSensitiveKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		// Original suffixes
		{"OPENROUTER_API_KEY", true},
		{"DB_SECRET", true},
		{"AUTH_TOKEN", true},
		{"ADMIN_PASSWORD", true},
		{"AWS_CREDENTIAL", true},
		{"my_api_key", true},

		// Broader suffixes added for hardening
		{"PRIVATE_KEY", true},
		{"ENCRYPTION_KEY", true},
		{"GOOGLE_APPLICATION_CREDENTIALS", true},
		{"MYSQL_PASSWD", true},
		{"BASIC_AUTH", true},
		{"GITHUB_PRIVATE", true},

		// Negative cases: should NOT be filtered
		{"PATH", false},
		{"HOME", false},
		{"GOPATH", false},
		{"SECRET_PROJECT_NAME", false},
		{"TOKENIZER_PATH", false},
		{"KEYBOARD_LAYOUT", false},
		{"AUTHOR", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := isSensitiveKey(tt.key)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("isSensitiveKey(%q) mismatch (-want +got):\n%s", tt.key, diff)
			}
		})
	}
}
