package discovery

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestWalkRecursiveWithFilters(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "keep.png"))
	mustWrite(t, filepath.Join(root, "drop.txt"))
	mustWrite(t, filepath.Join(root, "nested", "also.png"))

	var got []string
	err := Walk(context.Background(), []string{root}, true, Filters{
		Includes: []string{"*.png"},
		Excludes: []string{"drop*"},
	}, NewControl(), func(candidate Candidate) error {
		got = append(got, filepath.Base(candidate.Path))
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	slices.Sort(got)
	want := []string{"also.png", "keep.png"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestWalkNonRecursive(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "top.png"))
	mustWrite(t, filepath.Join(root, "nested", "child.png"))

	var got []string
	err := Walk(context.Background(), []string{root}, false, Filters{}, NewControl(), func(candidate Candidate) error {
		got = append(got, filepath.Base(candidate.Path))
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(got) != 1 || got[0] != "top.png" {
		t.Fatalf("unexpected files: %v", got)
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
