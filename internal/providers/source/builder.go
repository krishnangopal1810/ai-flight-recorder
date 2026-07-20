package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type BuildResult struct {
	CommitSHA    string
	DirtyDigest  string
	SourceDigest string
	Image        string
	ImageDigest  string
}

type Builder struct{ Git, OCI string }

func NewBuilder(gitBinary, ociBinary string) *Builder {
	if gitBinary == "" {
		gitBinary = "git"
	}
	if ociBinary == "" {
		ociBinary = "docker"
	}
	return &Builder{Git: gitBinary, OCI: ociBinary}
}

func (b *Builder) Build(ctx context.Context, revision string, entrypoint types.Entrypoint) (BuildResult, error) {
	root, err := b.output(ctx, "", b.Git, "rev-parse", "--show-toplevel")
	if err != nil {
		return BuildResult{}, types.NewError(types.ErrBuild, "current directory is not a Git worktree", "run replay from the candidate repository", err)
	}
	root = strings.TrimSpace(root)
	ref := revision
	if ref == "" {
		ref = "HEAD"
	}
	commit, err := b.output(ctx, root, b.Git, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return BuildResult{}, types.NewError(types.ErrBuild, "candidate revision cannot be resolved", "check --revision and fetch the ref", err)
	}
	commit = strings.TrimSpace(commit)
	diff, untracked, dirtyDigest, err := b.identity(ctx, root, revision == "")
	if err != nil {
		return BuildResult{}, err
	}
	if revision != "" {
		diff, untracked, dirtyDigest = nil, nil, ""
	}
	sourceDigest := sourceIdentity(commit, dirtyDigest)

	workspace, err := os.MkdirTemp("", "flight-source-")
	if err != nil {
		return BuildResult{}, types.NewError(types.ErrBuild, "cannot create isolated source worktree", "check temporary directory permissions", err)
	}
	if err := os.Remove(workspace); err != nil {
		return BuildResult{}, err
	}
	defer os.RemoveAll(workspace)
	if _, err := b.output(ctx, root, b.Git, "worktree", "add", "--detach", workspace, commit); err != nil {
		return BuildResult{}, types.NewError(types.ErrBuild, "cannot create isolated Git worktree", "clean stale worktrees with git worktree prune", err)
	}
	defer func() { _, _ = b.output(context.Background(), root, b.Git, "worktree", "remove", "--force", workspace) }()
	if len(diff) > 0 {
		command := exec.CommandContext(ctx, b.Git, "apply", "--binary", "--whitespace=nowarn", "-")
		command.Dir, command.Stdin = workspace, bytes.NewReader(diff)
		if output, applyErr := command.CombinedOutput(); applyErr != nil {
			return BuildResult{}, types.NewError(types.ErrBuild, "cannot materialize dirty candidate diff", "ensure the worktree diff applies cleanly to HEAD", fmt.Errorf("%s: %w", safeOutput(output), applyErr))
		}
	}
	for _, name := range untracked {
		if err := copyUntracked(root, workspace, name); err != nil {
			return BuildResult{}, err
		}
	}
	dockerfile, contextDir, err := buildPaths(workspace, entrypoint)
	if err != nil {
		return BuildResult{}, err
	}
	tag := "flight-candidate:" + strings.TrimPrefix(sourceDigest, "sha256:")[:20]
	args := []string{"build", "--pull=false", "--tag", tag, "--file", dockerfile, contextDir}
	if output, err := b.output(ctx, workspace, b.OCI, args...); err != nil {
		return BuildResult{}, types.NewError(types.ErrBuild, "candidate OCI image build failed", "fix the versioned Dockerfile and dependency inputs", fmt.Errorf("%s: %w", safeOutput([]byte(output)), err))
	}
	imageDigest, err := b.output(ctx, workspace, b.OCI, "image", "inspect", "--format", "{{.Id}}", tag)
	if err != nil {
		return BuildResult{}, types.NewError(types.ErrBuild, "cannot inspect candidate image digest", "verify the OCI image store", err)
	}
	return BuildResult{CommitSHA: commit, DirtyDigest: dirtyDigest, SourceDigest: sourceDigest, Image: tag, ImageDigest: strings.TrimSpace(imageDigest)}, nil
}

func (b *Builder) identity(ctx context.Context, root string, includeDirty bool) ([]byte, []string, string, error) {
	if !includeDirty {
		return nil, nil, "", nil
	}
	command := exec.CommandContext(ctx, b.Git, "diff", "--binary", "HEAD", "--")
	command.Dir = root
	diff, err := command.Output()
	if err != nil {
		return nil, nil, "", types.NewError(types.ErrBuild, "cannot read binary worktree diff", "check Git worktree health", err)
	}
	list := exec.CommandContext(ctx, b.Git, "ls-files", "--others", "--exclude-standard", "-z")
	list.Dir = root
	encoded, err := list.Output()
	if err != nil {
		return nil, nil, "", types.NewError(types.ErrBuild, "cannot list untracked candidate files", "check Git worktree health", err)
	}
	names := []string{}
	for _, raw := range bytes.Split(encoded, []byte{0}) {
		if len(raw) > 0 {
			names = append(names, string(raw))
		}
	}
	sort.Strings(names)
	hash := sha256.New()
	_, _ = hash.Write(diff)
	for _, name := range names {
		data, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if readErr != nil {
			return nil, nil, "", types.NewError(types.ErrBuild, "cannot hash untracked candidate file", "remove unreadable untracked files or fix permissions", readErr)
		}
		digest := sha256.Sum256(data)
		_, _ = io.WriteString(hash, "\x00"+name+"\x00"+hex.EncodeToString(digest[:]))
	}
	if len(diff) == 0 && len(names) == 0 {
		return diff, names, "", nil
	}
	return diff, names, "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func copyUntracked(root, workspace, name string) error {
	source := filepath.Join(root, filepath.FromSlash(name))
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return types.NewError(types.ErrBuild, "untracked candidate entry is not a regular file", "replace symlinks and special files with versioned regular files", nil)
	}
	destination := filepath.Join(workspace, filepath.FromSlash(name))
	if !within(workspace, destination) {
		return types.NewError(types.ErrBuild, "unsafe untracked candidate path", "remove path traversal entries", nil)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, data, info.Mode().Perm())
}

func buildPaths(workspace string, entrypoint types.Entrypoint) (string, string, error) {
	dockerfile := entrypoint.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	contextName := entrypoint.Context
	if contextName == "" {
		contextName = "."
	}
	dockerfile = filepath.Join(workspace, filepath.Clean(dockerfile))
	contextDir := filepath.Join(workspace, filepath.Clean(contextName))
	if !within(workspace, dockerfile) || !within(workspace, contextDir) {
		return "", "", types.NewError(types.ErrBuild, "OCI build path escapes the source worktree", "use repository-relative Dockerfile and context paths", nil)
	}
	if _, err := os.Stat(dockerfile); err != nil {
		return "", "", types.NewError(types.ErrBuild, "candidate Dockerfile is unavailable", "version the declared Dockerfile at the selected revision", err)
	}
	return dockerfile, contextDir, nil
}

func within(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
func sourceIdentity(commit, dirty string) string {
	sum := sha256.Sum256([]byte(commit + "\x00" + dirty))
	return "sha256:" + hex.EncodeToString(sum[:])
}
func safeOutput(value []byte) string {
	if len(value) > 4096 {
		value = value[len(value)-4096:]
	}
	return strings.TrimSpace(string(value))
}
func (b *Builder) output(ctx context.Context, dir, command string, args ...string) (string, error) {
	call := exec.CommandContext(ctx, command, args...)
	call.Dir = dir
	output, err := call.CombinedOutput()
	return string(output), err
}
