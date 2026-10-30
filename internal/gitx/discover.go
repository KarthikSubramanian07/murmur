package gitx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Discover finds the repository that dir is in without running git, which on Windows costs a
// tenth to a third of a second a process (#21): the nearest .git at or above dir, a directory or
// a file that names one ("gitdir: ..."), and the common directory the worktrees of a clone share,
// which a linked worktree's commondir file names. gitDir is the worktree's own git directory, where
// its HEAD is.
//
// It answers only the common case, the one git would answer the same way. ok is false, and the
// caller should ask git, when an environment variable changes how git finds a repository, when a
// directory on the way up is itself a git directory (a bare repository, or inside .git), when a
// directory on the way up to the .git is a symbolic link or a junction (git resolves those first,
// and may find another repository, one the link leads into), when a .git is neither a directory
// nor a regular file, or when anything can't be read.
func Discover(dir string) (gitDir, commonDir string, ok bool) {
	for _, v := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_CEILING_DIRECTORIES",
		"GIT_DISCOVERY_ACROSS_FILESYSTEM"} {
		if os.Getenv(v) != "" {
			return "", "", false
		}
	}
	if dir == "" {
		return "", "", false
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", "", false
	}
	for {
		if isGitDir(dir) {
			return "", "", false
		}
		dotGit := filepath.Join(dir, ".git")
		fi, err := os.Lstat(dotGit)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// A link here may lead into another repository. One above the .git doesn't
			// matter: git and this both find the same .git through it.
			if fi, err := os.Lstat(dir); err != nil || fi.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
				return "", "", false
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				return "", "", false
			}
			dir = parent
			continue
		case err != nil:
			return "", "", false
		case fi.IsDir():
			gitDir = dotGit
		case fi.Mode().IsRegular():
			b, err := os.ReadFile(dotGit)
			if err != nil {
				return "", "", false
			}
			path, found := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
			if !found {
				return "", "", false
			}
			gitDir = absFrom(dir, strings.TrimSpace(path))
		default:
			return "", "", false
		}
		if !isGitDir(gitDir) {
			return "", "", false
		}
		commonDir = gitDir
		b, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
		switch {
		case err == nil:
			commonDir = absFrom(gitDir, strings.TrimSpace(string(b)))
		case !errors.Is(err, fs.ErrNotExist):
			return "", "", false
		}
		return gitDir, commonDir, true
	}
}

// isGitDir says whether dir looks like a git directory: a HEAD file, and objects of its own or a
// commondir file pointing at a directory that has them.
func isGitDir(dir string) bool {
	if fi, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	for _, name := range []string{"commondir", "objects"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

func absFrom(base, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	return filepath.Clean(path)
}
