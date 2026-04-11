package envfile

import (
	"testing"
)

func TestParse_BasicKeyValue(t *testing.T) {
	input := "FOO=bar\nBAZ=qux"
	got := Parse(input)
	if got["FOO"] != "bar" {
		t.Errorf("FOO = %q, want %q", got["FOO"], "bar")
	}
	if got["BAZ"] != "qux" {
		t.Errorf("BAZ = %q, want %q", got["BAZ"], "qux")
	}
}

func TestParse_SkipsCommentsAndBlanks(t *testing.T) {
	input := "# this is a comment\n\nFOO=bar\n  # indented comment\nBAZ=qux\n"
	got := Parse(input)
	if len(got) != 2 {
		t.Errorf("len = %d, want 2; got %v", len(got), got)
	}
	if got["FOO"] != "bar" {
		t.Errorf("FOO = %q, want %q", got["FOO"], "bar")
	}
}

func TestParse_TrimsWhitespace(t *testing.T) {
	input := "  FOO  =  bar  \n"
	got := Parse(input)
	if got["FOO"] != "bar" {
		t.Errorf("FOO = %q, want %q", got["FOO"], "bar")
	}
}

func TestParse_StripsDoubleQuotes(t *testing.T) {
	input := `KEY="hello world"` + "\n"
	got := Parse(input)
	if got["KEY"] != "hello world" {
		t.Errorf("KEY = %q, want %q", got["KEY"], "hello world")
	}
}

func TestParse_StripsSingleQuotes(t *testing.T) {
	input := "KEY='hello world'\n"
	got := Parse(input)
	if got["KEY"] != "hello world" {
		t.Errorf("KEY = %q, want %q", got["KEY"], "hello world")
	}
}

func TestParse_MismatchedQuotesPreserved(t *testing.T) {
	input := `KEY="hello world'` + "\n"
	got := Parse(input)
	if got["KEY"] != `"hello world'` {
		t.Errorf("KEY = %q, want %q", got["KEY"], `"hello world'`)
	}
}

func TestParse_EmptyValue(t *testing.T) {
	input := "KEY=\n"
	got := Parse(input)
	if got["KEY"] != "" {
		t.Errorf("KEY = %q, want empty", got["KEY"])
	}
}

func TestParse_ValueWithEquals(t *testing.T) {
	input := "URL=postgres://host:5432/db?sslmode=disable\n"
	got := Parse(input)
	want := "postgres://host:5432/db?sslmode=disable"
	if got["URL"] != want {
		t.Errorf("URL = %q, want %q", got["URL"], want)
	}
}

func TestParse_WindowsLineEndings(t *testing.T) {
	input := "FOO=bar\r\nBAZ=qux\r\n"
	got := Parse(input)
	if got["FOO"] != "bar" {
		t.Errorf("FOO = %q, want %q", got["FOO"], "bar")
	}
	if got["BAZ"] != "qux" {
		t.Errorf("BAZ = %q, want %q", got["BAZ"], "qux")
	}
}

func TestParse_EmptyInput(t *testing.T) {
	got := Parse("")
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestParse_LineWithNoEquals(t *testing.T) {
	input := "not a key value pair\nFOO=bar\n"
	got := Parse(input)
	if len(got) != 1 {
		t.Errorf("expected 1 entry, got %d: %v", len(got), got)
	}
	if got["FOO"] != "bar" {
		t.Errorf("FOO = %q, want %q", got["FOO"], "bar")
	}
}

func TestParse_EmptyKeySkipped(t *testing.T) {
	input := "=value\nFOO=bar\n"
	got := Parse(input)
	if len(got) != 1 {
		t.Errorf("expected 1 entry, got %d: %v", len(got), got)
	}
}
