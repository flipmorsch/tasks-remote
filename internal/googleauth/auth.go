package googleauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	drive "google.golang.org/api/drive/v3"
)

const (
	serviceName  = "tasks-remote"
	tokenAccount = "google-oauth-token"
)

func ConfigFromCredentialsFile(path string) (*oauth2.Config, error) {
	if path == "" {
		return nil, fmt.Errorf("credentials path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read google credentials: %w", err)
	}
	config, err := google.ConfigFromJSON(data, drive.DriveAppdataScope)
	if err != nil {
		return nil, fmt.Errorf("parse google credentials: %w", err)
	}
	return config, nil
}

func Login(ctx context.Context, config *oauth2.Config) error {
	token, err := authorize(ctx, config)
	if err != nil {
		return err
	}
	return SaveToken(token)
}

func Logout() error {
	if err := keyring.Delete(serviceName, tokenAccount); err != nil && err != keyring.ErrNotFound {
		return fmt.Errorf("delete google token: %w", err)
	}
	return nil
}

func TokenSource(ctx context.Context, config *oauth2.Config) (oauth2.TokenSource, error) {
	token, err := LoadToken()
	if err != nil {
		return nil, err
	}
	source := config.TokenSource(ctx, token)
	return oauth2.ReuseTokenSource(token, persistTokenSource{source: source}), nil
}

func SaveToken(token *oauth2.Token) error {
	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("encode google token: %w", err)
	}
	if err := keyring.Set(serviceName, tokenAccount, string(data)); err != nil {
		return fmt.Errorf("store google token in OS keychain: %w", err)
	}
	return nil
}

func LoadToken() (*oauth2.Token, error) {
	data, err := keyring.Get(serviceName, tokenAccount)
	if err != nil {
		if err == keyring.ErrNotFound {
			return nil, fmt.Errorf("google account is not logged in: run `tasks login google -credentials <file>`")
		}
		return nil, fmt.Errorf("read google token from OS keychain: %w", err)
	}
	var token oauth2.Token
	if err := json.Unmarshal([]byte(data), &token); err != nil {
		return nil, fmt.Errorf("decode google token: %w", err)
	}
	return &token, nil
}

type persistTokenSource struct {
	source oauth2.TokenSource
}

func (s persistTokenSource) Token() (*oauth2.Token, error) {
	token, err := s.source.Token()
	if err != nil {
		return nil, err
	}
	if err := SaveToken(token); err != nil {
		return nil, err
	}
	return token, nil
}

func authorize(ctx context.Context, config *oauth2.Config) (*oauth2.Token, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start local oauth listener: %w", err)
	}
	defer listener.Close()

	state, err := randomState()
	if err != nil {
		return nil, err
	}
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
	}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "invalid oauth state", http.StatusBadRequest)
			errCh <- fmt.Errorf("invalid oauth state")
			return
		}
		if oauthErr := r.URL.Query().Get("error"); oauthErr != "" {
			http.Error(w, "oauth error", http.StatusBadRequest)
			errCh <- fmt.Errorf("oauth error: %s", oauthErr)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing oauth code", http.StatusBadRequest)
			errCh <- fmt.Errorf("missing oauth code")
			return
		}
		fmt.Fprintln(w, "Google login complete. You can return to the terminal.")
		codeCh <- code
	})
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	defer server.Shutdown(context.Background())

	config.RedirectURL = "http://" + listener.Addr().String()
	authURL := config.AuthCodeURL(state, oauth2.AccessTypeOffline)
	fmt.Fprintf(os.Stderr, "Open this URL to authorize Google Drive sync:\n%s\n", authURL)
	_ = openBrowser(authURL)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-errCh:
		return nil, err
	case code := <-codeCh:
		token, err := config.Exchange(ctx, code)
		if err != nil {
			return nil, fmt.Errorf("exchange oauth code: %w", err)
		}
		return token, nil
	}
}

func randomState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read oauth state randomness: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
