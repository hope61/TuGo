package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hope61/tugo/internal/config"
	"github.com/hope61/tugo/internal/testutil"
)

func TestFindRepo(t *testing.T) {
	repos := []config.Repo{{Dir: "/home/a/app"}, {Dir: "/home/b/app"}, {Dir: "/home/a/api"}}

	for name, want := range map[string]string{"api": "/home/a/api", "/home/a/app": "/home/a/app", "/home/b/app/": "/home/b/app"} {
		if repo, err := findRepo(repos, name); err != nil || repo.Dir != want {
			t.Fatalf("%q: got %q %v, want %q", name, repo.Dir, err, want)
		}
	}
	if _, err := findRepo(repos, "app"); err == nil || !strings.Contains(err.Error(), "more than one repo is called 'app', use its full path") {
		t.Fatalf("got %v", err)
	}
	if _, err := findRepo(repos, "web"); err == nil || !strings.Contains(err.Error(), "no repo 'web' in config.yaml, the repos are: app, app, api") {
		t.Fatalf("got %v", err)
	}
}

func TestSetPaused(t *testing.T) {
	dir := t.TempDir()
	setPaused(dir, "/srv/b", true)
	setPaused(dir, "/srv/with space", true)
	setPaused(dir, "/srv/b", true)
	if got := pausedRepos(dir); !slices.Equal(got, []string{"/srv/b", "/srv/with space"}) {
		t.Fatalf("got %q", got)
	}

	setPaused(dir, "/srv/b", false)
	setPaused(dir, "/srv/with space", false)
	if got := pausedRepos(dir); len(got) != 0 {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "paused")); err == nil {
		t.Fatal("the paused file should be removed when nothing is paused")
	}
	if err := setPaused(dir, "/srv/never-paused", false); err != nil {
		t.Fatalf("resuming a repo that isn't paused should not fail: %v", err)
	}
}

func TestCheckReposSkipsPausedRepos(t *testing.T) {
	r := testutil.NewRepos(t)
	other := testutil.NewRepos(t)
	before := r.Head(t)
	r.Push(t, "change")
	other.Push(t, "change")
	dir := t.TempDir()
	setPaused(dir, r.Srv, true)
	captureLog(t)

	checkRepos(dir, []config.Repo{repo(r.Srv, "touch ran.txt"), repo(other.Srv, "touch ran.txt")}, time.Minute)

	if r.Head(t) != before {
		t.Fatal("a paused repo was pulled")
	}
	if _, err := os.Stat(filepath.Join(r.Srv, "ran.txt")); err == nil {
		t.Fatal("a paused repo's commands ran")
	}
	if _, err := os.Stat(filepath.Join(other.Srv, "ran.txt")); err != nil {
		t.Fatal("the other repo should still be deployed")
	}
}
