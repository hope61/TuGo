package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/hope61/tugo/internal/config"
	"github.com/hope61/tugo/internal/github"
	"github.com/hope61/tugo/internal/logger"
)

func usage() {
	fmt.Fprintf(flag.CommandLine.Output(), `tugo keeps the git repos in config.yaml up to date.
Run without flags it starts the service loop.

Config file: %s

Repos:
  -s, --status                  show every repo at a glance
  -c, --check                   check all repos now and exit
  --pause repo ...              stop deploying repos (folder name or full path)
  --resume repo ...             deploy them again and pull what was pushed meanwhile

Logs:
  -l, --logs                    show the last 50 log lines
  -l -f                         follow the logs live

History:
  --history [n]                 show the last n deploys (default 20)

Config:
  -e                            edit config.yaml safely (like crontab -e)

Webhooks:
  --add-hook owner/repo ...     add the webhook to repos
  --remove-hook owner/repo ...  remove the webhook from repos

  -v, --version                 show the version
  -h, --help                    show this help
`, filepath.Join(logger.BinaryDir(), "config.yaml"))
}

func webhookSecret(binDir string, cfg *config.Config) string {
	path := filepath.Join(binDir, config.SecretFile)
	secret, created, err := config.LoadSecret(binDir, cfg.Secret)
	if errors.Is(err, fs.ErrPermission) {
		log.Fatalf("failed to load webhook secret: %v (run as the user tugo runs as, or with sudo)", err)
	}
	if err != nil {
		log.Fatalf("failed to load webhook secret: %v", err)
	}

	if cfg.Secret != "" {
		log.Printf("WARNING: tugo keeps the webhook secret in %s now, delete the secret line from config.yaml", path)
	} else if created {
		log.Printf("made a new webhook secret in %s, add it to your repos with tugo --add-hook owner/repo, repos that already have the hook need tugo --remove-hook owner/repo first", path)
	}
	return secret
}

func manageHooks(binDir string, cfg *config.Config, add bool, repos []string) {
	if len(repos) == 0 {
		log.Fatalf("no repos given, e.g. tugo --add-hook owner/repo")
	}
	if cfg.URL == "" {
		log.Fatalf("set url in config.yaml to the public webhook url")
	}
	var secret string
	if add {
		secret = webhookSecret(binDir, cfg)
	}

	failed := false
	for _, repo := range repos {
		var err error
		if add {
			err = github.AddHook(repo, cfg.URL, secret)
		} else {
			err = github.RemoveHook(repo, cfg.URL)
		}

		if err != nil {
			log.Printf("ERROR: %s — %v", repo, err)
			failed = true
			continue
		}
		log.Printf("done: %s", repo)
	}

	if failed {
		os.Exit(1)
	}
}
