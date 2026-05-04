package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit_FreshScaffold(t *testing.T) {
	dir := t.TempDir()
	if got := RunInit(context.Background(), []string{dir}); got != 0 {
		t.Fatalf("exit %d", got)
	}
	for _, expected := range []string{
		"rimsky-compose.yml",
		"deploy/docker-compose.yml",
		"deploy/store-filesystem.yml",
		"deploy/supervisor-config.yml",
		"graphs/example.yml",
		".gitignore",
	} {
		if _, err := os.Stat(filepath.Join(dir, expected)); err != nil {
			t.Errorf("missing %s: %v", expected, err)
		}
	}
	// .rimsky/ created.
	if info, err := os.Stat(filepath.Join(dir, ".rimsky")); err != nil || !info.IsDir() {
		t.Errorf(".rimsky/ missing")
	}
	// .gitignore contains /.rimsky/.
	raw, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(raw), "/.rimsky/") {
		t.Errorf(".gitignore missing entry: %s", raw)
	}
	// rimsky-compose.yml has the project name.
	rc, _ := os.ReadFile(filepath.Join(dir, "rimsky-compose.yml"))
	if !strings.Contains(string(rc), "project:") {
		t.Errorf("compose missing project: %s", rc)
	}
}

func TestInit_RefuseExisting(t *testing.T) {
	dir := t.TempDir()
	if got := RunInit(context.Background(), []string{dir}); got != 0 {
		t.Fatal("first run failed")
	}
	if got := RunInit(context.Background(), []string{dir}); got != 2 {
		t.Errorf("second run exit %d", got)
	}
}

func TestInit_Force(t *testing.T) {
	dir := t.TempDir()
	RunInit(context.Background(), []string{dir})
	if got := RunInit(context.Background(), []string{"--force", dir}); got != 0 {
		t.Errorf("force exit %d", got)
	}
}

func TestInit_AutoCreatesMissingDir(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "myproject")
	if got := RunInit(context.Background(), []string{target}); got != 0 {
		t.Fatalf("exit %d", got)
	}
	if _, err := os.Stat(filepath.Join(target, "rimsky-compose.yml")); err != nil {
		t.Errorf("missing scaffold: %v", err)
	}
}

func TestSanitizeProjectName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"my-project", "my-project"},
		{"My Project", "my-project"},
		{"123abc", "p123abc"},
		{"", "rimsky"},
	}
	for _, c := range cases {
		if got := sanitizeProjectName(c.in); got != c.want {
			t.Errorf("%q: got %q want %q", c.in, got, c.want)
		}
	}
}
