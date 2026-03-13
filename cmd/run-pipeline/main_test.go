package main

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSandboxName_FromUUID(t *testing.T) {
	id := uuid.MustParse("a1b2c3d4-e5f6-7890-abcd-ef1234567890")
	got := sandboxName(id)
	want := "attractor-a1b2c3d4"
	if got != want {
		t.Errorf("sandboxName(%s) = %q, want %q", id, got, want)
	}
}

func TestSandboxName_NilUUID(t *testing.T) {
	got := sandboxName(uuid.Nil)
	if !strings.HasPrefix(got, "attractor-") {
		t.Errorf("sandboxName(Nil) = %q, want prefix 'attractor-'", got)
	}
	if len(got) != len("attractor-")+8 {
		t.Errorf("sandboxName(Nil) = %q, want 18-char result (attractor- + 8 hex)", got)
	}
}

func TestSandboxName_NilUUID_Unique(t *testing.T) {
	a := sandboxName(uuid.Nil)
	b := sandboxName(uuid.Nil)
	if a == b {
		t.Errorf("two calls with Nil should produce different names, both got %q", a)
	}
}
