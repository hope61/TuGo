package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSecretMakesOne(t *testing.T) {
	dir := t.TempDir()
	secret, created, err := LoadSecret(dir, "")
	if err != nil || !created || len(secret) != 64 {
		t.Fatalf("got %q created=%v err=%v", secret, created, err)
	}

	stat, err := os.Stat(filepath.Join(dir, SecretFile))
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("secret file should be 0600: %v %v", stat, err)
	}

	again, created, err := LoadSecret(dir, "")
	if err != nil || created || again != secret {
		t.Fatalf("second load should read the same secret: %q created=%v err=%v", again, created, err)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestLoadSecretTrimsNewline(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, SecretFile), []byte("s3cret\n"), 0600)
	if secret, _, err := LoadSecret(dir, ""); err != nil || secret != "s3cret" {
		t.Fatalf("got %q %v", secret, err)
	}
}

func TestLoadSecretMovesOldSecret(t *testing.T) {
	dir := t.TempDir()
	secret, created, err := LoadSecret(dir, "s3cret")
	if err != nil || !created || secret != "s3cret" {
		t.Fatalf("got %q created=%v err=%v", secret, created, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, SecretFile)); string(data) != "s3cret\n" {
		t.Fatalf("file has %q", data)
	}

	// the same secret in both places is fine, the line in config.yaml is just left over
	if secret, _, err := LoadSecret(dir, "s3cret"); err != nil || secret != "s3cret" {
		t.Fatalf("got %q %v", secret, err)
	}
}

func TestLoadSecretErrors(t *testing.T) {
	tests := []struct {
		name, file, old, want string
	}{
		{"empty file", " \n", "", "is empty"},
		{"different secrets", "s3cret\n", "other", "have different secrets"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, SecretFile), []byte(tt.file), 0600)
			_, _, err := LoadSecret(dir, tt.old)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q, got %v", tt.want, err)
			}
		})
	}
}
