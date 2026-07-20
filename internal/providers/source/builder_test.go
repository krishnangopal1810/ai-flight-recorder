package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAFRT12DirtyIdentityIncludesBinaryDiffAndSortedUntrackedFiles(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "tracked"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "tracked")
	runGit(t, root, "commit", "-qm", "initial")
	if err := os.WriteFile(filepath.Join(root, "tracked"), []byte{0, 1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "z-untracked"), []byte("z"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a-untracked"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	builder := NewBuilder("git", "docker")
	_, names, first, err := builder.identity(context.Background(), root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "a-untracked" || first == "" {
		t.Fatalf("unexpected identity: %#v %s", names, first)
	}
	_, _, second, err := builder.identity(context.Background(), root, true)
	if err != nil || first != second {
		t.Fatalf("identity is not deterministic: %s %s %v", first, second, err)
	}
}

func runGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, output, err)
	}
}
