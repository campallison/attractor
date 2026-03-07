package pipeline

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestContext_SetGet(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    string
		getKey   string
		fallback string
		want     string
	}{
		{name: "set and get", key: "k", value: "v", getKey: "k", fallback: "default", want: "v"},
		{name: "missing key", key: "k", value: "v", getKey: "missing", fallback: "default", want: "default"},
		{name: "empty value", key: "k", value: "", getKey: "k", fallback: "default", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext()
			ctx.Set(tt.key, tt.value)
			got := ctx.Get(tt.getKey, tt.fallback)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Get mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestContext_GetString(t *testing.T) {
	ctx := NewContext()
	ctx.Set("fruit", "apple")
	if diff := cmp.Diff("apple", ctx.GetString("fruit")); diff != "" {
		t.Errorf("GetString mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("", ctx.GetString("missing")); diff != "" {
		t.Errorf("GetString missing key mismatch (-want +got):\n%s", diff)
	}
}

func TestContext_Snapshot(t *testing.T) {
	ctx := NewContext()
	ctx.Set("a", "1")
	ctx.Set("b", "2")
	snap := ctx.Snapshot()
	want := map[string]string{"a": "1", "b": "2"}
	if diff := cmp.Diff(want, snap); diff != "" {
		t.Errorf("Snapshot mismatch (-want +got):\n%s", diff)
	}
	// Mutation of snapshot must not affect original.
	snap["c"] = "3"
	if ctx.GetString("c") != "" {
		t.Error("snapshot mutation leaked into context")
	}
}

func TestContext_Clone(t *testing.T) {
	ctx := NewContext()
	ctx.Set("x", "1")
	ctx.AppendLog("entry-1")

	clone := ctx.Clone()

	// Clone has the same values.
	if diff := cmp.Diff("1", clone.GetString("x")); diff != "" {
		t.Errorf("clone value mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"entry-1"}, clone.Logs()); diff != "" {
		t.Errorf("clone logs mismatch (-want +got):\n%s", diff)
	}

	// Mutations are isolated.
	clone.Set("x", "2")
	clone.Set("y", "3")
	if diff := cmp.Diff("1", ctx.GetString("x")); diff != "" {
		t.Errorf("original should be unchanged (-want +got):\n%s", diff)
	}
	if ctx.GetString("y") != "" {
		t.Error("clone mutation leaked into original")
	}
}

func TestContext_ApplyUpdates(t *testing.T) {
	ctx := NewContext()
	ctx.Set("a", "1")
	ctx.ApplyUpdates(map[string]string{"a": "overwritten", "b": "new"})
	want := map[string]string{"a": "overwritten", "b": "new"}
	if diff := cmp.Diff(want, ctx.Snapshot()); diff != "" {
		t.Errorf("ApplyUpdates mismatch (-want +got):\n%s", diff)
	}
}

func TestContext_ApplyUpdatesNil(t *testing.T) {
	ctx := NewContext()
	ctx.Set("a", "1")
	ctx.ApplyUpdates(nil) // should be a no-op
	if diff := cmp.Diff(map[string]string{"a": "1"}, ctx.Snapshot()); diff != "" {
		t.Errorf("nil ApplyUpdates should be no-op (-want +got):\n%s", diff)
	}
}

func TestContext_Logs(t *testing.T) {
	ctx := NewContext()
	ctx.AppendLog("first")
	ctx.AppendLog("second")
	want := []string{"first", "second"}
	if diff := cmp.Diff(want, ctx.Logs()); diff != "" {
		t.Errorf("Logs mismatch (-want +got):\n%s", diff)
	}
}
