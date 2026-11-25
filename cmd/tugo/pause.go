package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hope61/tugo/internal/config"
	"github.com/hope61/tugo/internal/logger"
)

// paused repos are kept in a file because tugo --pause and the service are separate processes
func pausedRepos(binDir string) []string {
	data, _ := os.ReadFile(filepath.Join(binDir, "paused"))
	var dirs []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			dirs = append(dirs, line)
		}
	}
	return dirs
}

func setPaused(binDir, dir string, paused bool) error {
	dirs := slices.DeleteFunc(pausedRepos(binDir), func(d string) bool { return d == dir })
	if paused {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)

	path := filepath.Join(binDir, "paused")
	if len(dirs) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return os.WriteFile(path, []byte(strings.Join(dirs, "\n")+"\n"), 0644)
}

// findRepo matches a repo in the config by its full path or its folder name
func findRepo(repos []config.Repo, name string) (config.Repo, error) {
	var found []config.Repo
	for _, repo := range repos {
		if repo.Dir == name || filepath.Clean(repo.Dir) == filepath.Clean(name) || filepath.Base(repo.Dir) == name {
			found = append(found, repo)
		}
	}

	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		var names []string
		for _, repo := range repos {
			names = append(names, filepath.Base(repo.Dir))
		}
		return config.Repo{}, fmt.Errorf("no repo '%s' in config.yaml, the repos are: %s", name, strings.Join(names, ", "))
	default:
		return config.Repo{}, fmt.Errorf("more than one repo is called '%s', use its full path", name)
	}
}

func pauseRepos(binDir string, cfg *config.Config, resume bool, names []string) {
	if len(names) == 0 {
		log.Fatalf("no repos given, e.g. tugo --pause my-app")
	}

	var repos []config.Repo
	for _, name := range names {
		repo, err := findRepo(cfg.Repos, name)
		if err != nil {
			log.Fatal(err)
		}
		repos = append(repos, repo)
	}

	logFile := logger.Setup(binDir)
	defer logFile.Close()

	for _, repo := range repos {
		if err := setPaused(binDir, repo.Dir, !resume); err != nil {
			log.Fatalf("failed to save paused repos: %v", err)
		}
		if resume {
			log.Printf("resumed %s", repo.Dir)
		} else {
			log.Printf("paused %s, it won't be deployed until tugo --resume", repo.Dir)
		}
	}

	// pull anything that was pushed while the repos were paused
	if resume {
		checkRepos(binDir, repos, cfg.Timeout)
	}
}
