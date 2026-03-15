package consistency

import (
	"strings"
	"testing"
)

func TestCheckStoreInterface_AllMatched(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "store.go", `package store

import "context"

type Store interface {
	GetUser(ctx context.Context, id string) (string, error)
	CreateUser(ctx context.Context, name string) error
}
`)
	writeFile(t, dir, "handler.go", `package handler

import (
	"context"
	"example/store"
)

type Handler struct {
	Store store.Store
}

func (h *Handler) Handle() {
	ctx := context.Background()
	h.Store.GetUser(ctx, "1")
	h.Store.CreateUser(ctx, "alice")
}
`)

	result := CheckStoreInterface(dir)

	if !result.Passed {
		t.Errorf("expected PASS, got FAIL:\n%s", result)
	}
	if !strings.Contains(result.Summary, "2 interface methods") {
		t.Errorf("unexpected summary: %s", result.Summary)
	}
}

func TestCheckStoreInterface_MissingMethod(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "store.go", `package store

import "context"

type Store interface {
	GetUser(ctx context.Context, id string) (string, error)
}
`)
	writeFile(t, dir, "handler.go", `package handler

import (
	"context"
	"example/store"
)

type Handler struct {
	Store store.Store
}

func (h *Handler) Handle() {
	ctx := context.Background()
	h.Store.GetUser(ctx, "1")
	h.Store.DeleteUser(ctx, "1")
}
`)

	result := CheckStoreInterface(dir)

	if result.Passed {
		t.Error("expected FAIL, got PASS")
	}

	var errors []Finding
	for _, f := range result.Findings {
		if f.Severity == SeverityError {
			errors = append(errors, f)
		}
	}
	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errors), errors)
	}
	if !strings.Contains(errors[0].Message, "DeleteUser") {
		t.Errorf("expected error about DeleteUser, got: %s", errors[0].Message)
	}
}

func TestCheckStoreInterface_UnusedMethod(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "store.go", `package store

import "context"

type Store interface {
	GetUser(ctx context.Context, id string) (string, error)
	DeleteUser(ctx context.Context, id string) error
}
`)
	writeFile(t, dir, "handler.go", `package handler

import (
	"context"
	"example/store"
)

type Handler struct {
	Store store.Store
}

func (h *Handler) Handle() {
	ctx := context.Background()
	h.Store.GetUser(ctx, "1")
}
`)

	result := CheckStoreInterface(dir)

	if !result.Passed {
		t.Error("expected PASS (unused methods are warnings)")
	}

	var warnings []Finding
	for _, f := range result.Findings {
		if f.Severity == SeverityWarning {
			warnings = append(warnings, f)
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if !strings.Contains(warnings[0].Message, "DeleteUser") {
		t.Errorf("expected warning about DeleteUser, got: %s", warnings[0].Message)
	}
}

func TestCheckStoreInterface_NoInterfaces(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "main.go", `package main
func main() {}
`)

	result := CheckStoreInterface(dir)

	if !result.Passed {
		t.Errorf("expected PASS with no interfaces, got FAIL")
	}
}

func TestCheckStoreInterface_SamePackageInterface(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "types.go", `package app

import "context"

type Repository interface {
	Find(ctx context.Context, id string) error
}

type Service struct {
	Repo Repository
}

func (s *Service) DoWork() {
	s.Repo.Find(nil, "1")
}
`)

	result := CheckStoreInterface(dir)

	if !result.Passed {
		t.Errorf("expected PASS (same-package interface), got FAIL:\n%s", result)
	}
}

func TestCheckStoreInterface_PointerField(t *testing.T) {
	dir := t.TempDir()

	// Pointer to an interface isn't idiomatic Go, but the check should
	// still work if someone does it.
	writeFile(t, dir, "types.go", `package app

type Cache interface {
	Get(key string) string
}

type Handler struct {
	Cache *Cache
}

func (h *Handler) Handle() {
	h.Cache.Get("key")
}
`)

	// Note: *Cache as a field type is unusual. Our type name extraction
	// strips the pointer, so it should still match the Cache interface.
	result := CheckStoreInterface(dir)

	if !result.Passed {
		t.Errorf("expected PASS, got FAIL:\n%s", result)
	}
}
