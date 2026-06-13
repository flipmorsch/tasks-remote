package unlock

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
	"golang.org/x/term"
)

const (
	ServiceName = "tasks-remote"
	EnvSecret   = "TASKS_REMOTE_SECRET"
)

func Load(dbPath string) (string, error) {
	if secret := os.Getenv(EnvSecret); secret != "" {
		return secret, nil
	}
	secret, err := keyring.Get(ServiceName, account(dbPath))
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", fmt.Errorf("device is locked: run `tasks-remote -db %q unlock` or set %s", dbPath, EnvSecret)
		}
		return "", fmt.Errorf("read OS keychain: %w", err)
	}
	if secret == "" {
		return "", fmt.Errorf("device is locked: cached unlock material is empty")
	}
	return secret, nil
}

func Save(dbPath, secret string) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return fmt.Errorf("recovery secret is required")
	}
	if err := keyring.Set(ServiceName, account(dbPath), secret); err != nil {
		return fmt.Errorf("write OS keychain: %w", err)
	}
	return nil
}

func Clear(dbPath string) error {
	if err := keyring.Delete(ServiceName, account(dbPath)); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("clear OS keychain: %w", err)
	}
	return nil
}

func Prompt(label string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s: ", label)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		secret, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("read recovery secret: %w", err)
		}
		return strings.TrimSpace(string(secret)), nil
	}
	var secret string
	if _, err := fmt.Fscanln(os.Stdin, &secret); err != nil {
		return "", fmt.Errorf("read recovery secret: %w", err)
	}
	return strings.TrimSpace(secret), nil
}

func account(dbPath string) string {
	sum := sha256.Sum256([]byte(dbPath))
	return "db-" + hex.EncodeToString(sum[:])
}
