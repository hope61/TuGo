package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const SecretFile = "webhook.secret"

// LoadSecret reads dir/webhook.secret and makes it when it's missing.
// old is a secret still written in config.yaml, it's moved to the file so hooks already on GitHub keep working.
func LoadSecret(dir, old string) (secret string, created bool, err error) {
	path := filepath.Join(dir, SecretFile)
	data, err := os.ReadFile(path)
	if err == nil {
		secret = strings.TrimSpace(string(data))
		if secret == "" {
			return "", false, fmt.Errorf("%s is empty, delete it and tugo makes a new one", path)
		}
		if old != "" && old != secret {
			return "", false, fmt.Errorf("config.yaml and %s have different secrets, delete the secret line from config.yaml", path)
		}
		return secret, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}

	secret = old
	if secret == "" {
		b := make([]byte, 32)
		rand.Read(b)
		secret = hex.EncodeToString(b)
	}

	// CreateTemp makes the file 0600
	tmp, err := os.CreateTemp(dir, "."+SecretFile+"-*")
	if err != nil {
		return "", false, err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(secret + "\n")
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", false, err
	}

	// under sudo, give the file to whoever owns the dir so the service can still read it
	if os.Geteuid() == 0 {
		stat, err := os.Stat(dir)
		if err != nil {
			return "", false, err
		}
		owner := stat.Sys().(*syscall.Stat_t)
		if err := os.Chown(tmp.Name(), int(owner.Uid), int(owner.Gid)); err != nil {
			return "", false, err
		}
	}

	// link fails if the file exists, so two tugos starting at once can't make two secrets
	err = os.Link(tmp.Name(), path)
	if errors.Is(err, os.ErrExist) {
		return LoadSecret(dir, old)
	}
	if err != nil {
		return "", false, err
	}
	return secret, true, nil
}
