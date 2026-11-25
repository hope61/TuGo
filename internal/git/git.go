package git

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"
)

func CurrentBranch(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get current branch: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

func Pull(dir, remote, branch string) ([]string, error) {
	before := head(dir)

	args := []string{"pull", "--ff-only", remote}
	if branch != "" {
		args = append(args, branch)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	output, err := cmd.CombinedOutput()
	out := strings.TrimSpace(string(output))

	if err != nil && strings.Contains(out, "Not possible to fast-forward") {
		if err := resetToRewrittenRemote(dir, remote, branch, before); err != nil {
			return nil, err
		}
		err = nil
	}

	if err != nil {
		switch {
		case strings.Contains(out, "does not appear to be a git repository"):
			return nil, fmt.Errorf("remote '%s' does not exist or is not a valid repository", remote)
		case strings.Contains(out, "Could not resolve host"):
			return nil, fmt.Errorf("cannot reach remote '%s' — host not found (check your URL or internet connection)", remote)
		case strings.Contains(out, "Connection refused"):
			return nil, fmt.Errorf("cannot reach remote '%s' — connection refused", remote)
		case strings.Contains(out, "Connection timed out"):
			return nil, fmt.Errorf("cannot reach remote '%s' — connection timed out", remote)
		case strings.Contains(out, "Authentication failed") || strings.Contains(out, "Permission denied"):
			return nil, fmt.Errorf("authentication failed for remote '%s' — check your credentials", remote)
		case strings.Contains(out, "couldn't find remote ref"):
			return nil, fmt.Errorf("branch '%s' does not exist on remote '%s'", branch, remote)
		default:
			return nil, fmt.Errorf("git pull failed: %w", err)
		}
	}

	after := head(dir)
	if after == "" || after == before {
		return nil, nil
	}

	commits, err := newCommits(dir, before, after)
	if err != nil || len(commits) == 0 {
		return []string{after[:7]}, nil
	}
	return commits, nil
}

// After a force push the server copy can't fast-forward anymore. Reset it to the remote only when
// nothing made on the server can be lost: no uncommitted changes, and HEAD is a commit the remote had before.
func resetToRewrittenRemote(dir, remote, branch, before string) error {
	if branch == "" {
		current, err := CurrentBranch(dir)
		if err != nil {
			return err
		}
		branch = current
	}
	target := remote + "/" + branch

	if changes, err := gitOutput(dir, "status", "--porcelain", "--untracked-files=no"); err != nil || changes != "" {
		return fmt.Errorf("local history differs from %s and there are uncommitted changes, commit or drop them and run git reset --hard %s", target, target)
	}

	past, err := gitOutput(dir, "rev-list", "--walk-reflogs", "refs/remotes/"+target)
	if err != nil || !slices.Contains(strings.Fields(past), before) {
		return fmt.Errorf("local history differs from %s and has commits that were never on the remote, run git reset --hard %s if you want to drop them", target, target)
	}

	if _, err := gitOutput(dir, "reset", "-q", "--hard", target); err != nil {
		return fmt.Errorf("failed to reset to %s: %w", target, err)
	}
	log.Printf("WARNING: %s was force pushed, reset %s from %s to match it", target, dir, before[:7])
	return nil
}

// Info returns the short hash and subject of the checked out commit, and whether tracked files have uncommitted changes
func Info(dir string) (commit, subject string, dirty bool, err error) {
	out, err := gitOutput(dir, "log", "-1", "--format=%h%x00%s")
	if err != nil {
		return "", "", false, fmt.Errorf("not a git repo or it has no commits")
	}
	commit, subject, _ = strings.Cut(out, "\x00")
	changes, _ := gitOutput(dir, "status", "--porcelain", "--untracked-files=no")
	return commit, subject, changes != "", nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.Output()
	return strings.TrimSpace(string(output)), err
}

func head(dir string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir

	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func newCommits(dir, from, to string) ([]string, error) {
	rev := to
	if from != "" {
		rev = from + ".." + to
	}

	cmd := exec.Command("git", "log", "--format=%h%x00%s%x00%an", rev)
	cmd.Dir = dir

	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var commits []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		parts := strings.SplitN(line, "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		subject := []rune(parts[1])
		if len(subject) > 72 {
			subject = append(subject[:69], []rune("...")...)
		}
		commits = append(commits, fmt.Sprintf("%s %s (%s)", parts[0], string(subject), parts[2]))
	}
	return commits, nil
}

func RunCommand(dir, command string, timeout time.Duration) error {
	log.Printf("running: %s", command)

	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("empty command in config")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 10 * time.Second

	output, err := cmd.CombinedOutput()
	out := strings.TrimSpace(string(output))

	if err != nil {
		lines := lastLines(out, 30)
		if lines != "" {
			log.Printf("command output (last 30 lines):\n%s", lines)
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("command timed out after %s and was killed: %s", timeout, command)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			// sh exits with 127 when a program in the command does not exist
			if exitErr.ExitCode() == 127 {
				return fmt.Errorf("command not found: %s — check your config or install it", command)
			}
			return fmt.Errorf("command failed with exit code %d: %s", exitErr.ExitCode(), command)
		}
		return fmt.Errorf("command failed: %s — %w", command, err)
	}

	log.Printf("command finished successfully")
	return nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return "...\n" + strings.Join(lines[len(lines)-n:], "\n")
}
