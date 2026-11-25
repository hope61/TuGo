package main

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hope61/tugo/internal/config"
	"github.com/hope61/tugo/internal/history"
	"github.com/hope61/tugo/internal/testutil"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestParseService(t *testing.T) {
	running := parseService("LoadState=loaded\nActiveState=active\nSubState=running\nActiveEnterTimestamp=@1764000000\n")
	if running.state != "running" || !running.since.Equal(time.Unix(1764000000, 0)) {
		t.Fatalf("got %+v", running)
	}
	for out, want := range map[string]string{
		"LoadState=not-found\nActiveState=inactive\nSubState=dead\n":      "missing",
		"LoadState=loaded\nActiveState=activating\nSubState=auto-restart": "crashing",
		"LoadState=loaded\nActiveState=failed\nSubState=failed":           "failed",
		"LoadState=loaded\nActiveState=inactive\nSubState=dead":           "stopped",
		"": "",
	} {
		if got := parseService(out).state; got != want {
			t.Fatalf("%q: got %q, want %q", out, got, want)
		}
	}
}

func TestTimeFormats(t *testing.T) {
	for d, want := range map[time.Duration]string{30 * time.Second: "just now", 5 * time.Minute: "5m ago", 3 * time.Hour: "3h ago", 50 * time.Hour: "2d ago"} {
		if got := ago(d); got != want {
			t.Fatalf("ago(%s) = %q, want %q", d, got, want)
		}
	}
	for d, want := range map[time.Duration]string{4 * time.Minute: "4m", 125 * time.Minute: "2h 5m", 76 * time.Hour: "3d 4h"} {
		if got := uptime(d); got != want {
			t.Fatalf("uptime(%s) = %q, want %q", d, got, want)
		}
	}
	for d, want := range map[time.Duration]string{5 * time.Minute: "5m", time.Hour: "1h", 90 * time.Second: "1m30s", 30 * time.Second: "30s"} {
		if got := shortDuration(d); got != want {
			t.Fatalf("shortDuration(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestShowStatus(t *testing.T) {
	ok, failed, pausedRepo, fresh, wrong, dirty := testutil.NewRepos(t), testutil.NewRepos(t), testutil.NewRepos(t), testutil.NewRepos(t), testutil.NewRepos(t), testutil.NewRepos(t)
	testutil.Git(t, wrong.Srv, "checkout", "-q", "-b", "develop")
	os.WriteFile(filepath.Join(dirty.Src, "a.txt"), []byte("1"), 0644)
	testutil.Git(t, dirty.Src, "add", "a.txt")
	dirty.Push(t, "add a")
	testutil.Git(t, dirty.Srv, "pull", "-q")
	os.WriteFile(filepath.Join(dirty.Srv, "a.txt"), []byte("edited"), 0644)

	dir := t.TempDir()
	now := time.Now()
	history.Add(dir, history.Entry{Time: now.Add(-2 * time.Hour), Repo: ok.Srv, Commit: "aaaaaaa", Commits: 1, Took: 5 * time.Second})
	history.Add(dir, history.Entry{Time: now.Add(-3 * 24 * time.Hour), Repo: failed.Srv, Commit: "bbbbbbb", Commits: 1, Error: "command failed with exit code 1: npm run build", Took: 3 * time.Second})
	history.Add(dir, history.Entry{Time: now.Add(-24 * time.Hour), Repo: pausedRepo.Srv, Commit: "ccccccc", Commits: 1, Took: 9 * time.Second})
	setPaused(dir, pausedRepo.Srv, true)

	wrongRepo := config.Repo{Dir: wrong.Srv, Mode: "webhook", Branch: "main"}
	cfg := &config.Config{Listen: ":9000", Interval: 5 * time.Minute, Repos: []config.Repo{
		{Dir: ok.Srv, Mode: "webhook"}, {Dir: failed.Srv, Mode: "webhook"}, {Dir: pausedRepo.Srv, Mode: "poll"},
		{Dir: fresh.Srv, Mode: "webhook"}, wrongRepo, {Dir: dirty.Srv, Mode: "webhook"}, {Dir: "/nope/old-site", Mode: "webhook"},
	}}

	out := captureStdout(t, func() { showStatus(dir, cfg, service{state: "running", since: now.Add(-76 * time.Hour)}, now) })

	for _, want := range []string{
		" TuGo  dev\n",
		" ● running for 3d 4h  ·  webhook on :9000  ·  polling every 5m\n",
		"✓ 2h ago   5s\n",
		"✗ 3d ago   3s\n",
		"    └ command failed with exit code 1: npm run build\n",
		"    └ paused, tugo --resume srv to deploy it again\n",
		"    └ on 'develop' but the config expects 'main', not deploying\n",
		"    └ uncommitted changes in the folder, a force push can't be fixed automatically\n",
		"    └ the folder /nope/old-site doesn't exist\n",
		" 7 repos  ·  1 ok  ·  2 failed  ·  1 paused  ·  2 need a look  ·  1 never deployed\n",
		" init\x20",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatal("no colors when stdout is not a terminal")
	}

	var modeAt []int
	for _, line := range strings.Split(out, "\n") {
		if (strings.HasPrefix(line, " ● ") || strings.HasPrefix(line, " ○ ")) && !strings.Contains(line, "running for") {
			at := strings.Index(line, "  webhook")
			if at < 0 {
				at = strings.Index(line, "  poll")
			}
			modeAt = append(modeAt, at)
		}
	}
	if len(modeAt) != 7 || slices.Min(modeAt) != slices.Max(modeAt) {
		t.Fatalf("the MODE column does not line up, positions %v:\n%s", modeAt, out)
	}
}

func TestShowStatusServiceStates(t *testing.T) {
	cfg := &config.Config{Interval: time.Minute, Repos: []config.Repo{{Dir: "/nope", Mode: "poll"}}}
	for state, want := range map[string]string{
		"crashing": "● crashing and restarting, see tugo -l",
		"failed":   "● stopped after a crash, see tugo -l",
		"stopped":  "● stopped, start it with sudo systemctl start tugo",
		"missing":  "○ not installed as a service, see sudo make service",
	} {
		out := captureStdout(t, func() { showStatus(t.TempDir(), cfg, service{state: state}, time.Now()) })
		if !strings.Contains(out, want) || !strings.Contains(out, " 1 repo  ·  1 failed\n") {
			t.Fatalf("%s: missing %q:\n%s", state, want, out)
		}
	}
}
