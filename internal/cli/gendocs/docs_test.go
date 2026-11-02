package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestGenerateDocsUpToDate(t *testing.T) {
	dir := t.TempDir()
	// a stale doc for a removed command should be removed
	stale := filepath.Join(dir, docsPrefix+"-removed.md")
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := generateDocs(dir); err != nil {
		t.Fatalf("generateDocs() error = %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale doc %s was not removed", stale)
	}

	generated := docFiles(t, dir)
	committed := docFiles(t, docsDir)
	if !slices.Equal(generated, committed) {
		t.Fatalf("docs in %s are out of date, run \"go generate ./...\": got %v, want %v", docsDir, committed, generated)
	}

	for _, name := range generated {
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(docsDir, name))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("%s is out of date, run \"go generate ./...\"", name)
			}
		})
	}
}

// docFiles returns the sorted base names of the generated docs in dir.
func docFiles(t *testing.T, dir string) []string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(dir, docsPrefix+"*.md"))
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, filepath.Base(m))
	}
	slices.Sort(names)

	return names
}

func TestDocFilename(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"am-autotask", "am-autotask.md"},
		{"am-autotask serve", "am-autotask-serve.md"},
		{"am-autotask lookup picklists", "am-autotask-lookup-picklists.md"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := docFilename(tt.path); got != tt.want {
				t.Errorf("docFilename(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestDocLink(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"am-autotask.md", "am-autotask.md"},
		{"am-autotask_lookup.md", "am-autotask-lookup.md"},
		{"am-autotask_lookup_picklists.md", "am-autotask-lookup-picklists.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := docLink(tt.name); got != tt.want {
				t.Errorf("docLink(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
