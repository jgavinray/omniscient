package drive

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
)

// loadOAuthConfig reads the OAuth2 client credentials JSON file and returns
// an oauth2.Config configured for Google Drive read-only access.
func loadOAuthConfig(credentialsPath string) (*oauth2.Config, error) {
	data, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("reading credentials file %s: %w", credentialsPath, err)
	}

	config, err := google.ConfigFromJSON(data, drive.DriveReadonlyScope)
	if err != nil {
		return nil, fmt.Errorf("parsing credentials JSON: %w", err)
	}

	config.RedirectURL = "http://localhost:8085/callback"

	return config, nil
}

// loadToken reads a saved OAuth2 token from tokenPath. If the file does not
// exist, it returns (nil, nil) so the caller can initiate the auth flow.
func loadToken(tokenPath string) (*oauth2.Token, error) {
	f, err := os.Open(tokenPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening token file %s: %w", tokenPath, err)
	}
	defer f.Close()

	var token oauth2.Token
	if err := json.NewDecoder(f).Decode(&token); err != nil {
		return nil, fmt.Errorf("decoding token JSON from %s: %w", tokenPath, err)
	}

	return &token, nil
}

// saveToken writes the OAuth2 token as JSON to tokenPath. It creates any
// necessary parent directories and sets file permissions to 0600.
func saveToken(tokenPath string, token *oauth2.Token) error {
	dir := filepath.Dir(tokenPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating token directory %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling token: %w", err)
	}

	if err := os.WriteFile(tokenPath, data, 0600); err != nil {
		return fmt.Errorf("writing token file %s: %w", tokenPath, err)
	}

	slog.Info("saved OAuth2 token", "path", tokenPath)
	return nil
}

// generateState creates a random state parameter for CSRF protection.
func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random state: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// RunAuthFlow performs the full interactive OAuth2 browser consent flow.
// It starts a temporary HTTP server on localhost:8085 to capture the callback,
// opens the user's browser to the Google consent page, exchanges the
// authorization code for a token, and saves it to tokenPath.
func RunAuthFlow(credentialsPath, tokenPath string) (*oauth2.Token, error) {
	config, err := loadOAuthConfig(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("loading OAuth config: %w", err)
	}

	state, err := generateState()
	if err != nil {
		return nil, err
	}

	authURL := config.AuthCodeURL(state, oauth2.AccessTypeOffline)

	// Channel to receive the authorization code from the callback handler.
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		// Verify the state parameter to prevent CSRF.
		if r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid state parameter", http.StatusBadRequest)
			errCh <- fmt.Errorf("OAuth callback: state mismatch (possible CSRF)")
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			rawProviderError := r.URL.Query().Get("error")
			if rawProviderError != "" {
				slog.Warn("OAuth2 provider returned error", "error", rawProviderError)
			}
			http.Error(w, "Authorization failed. Check application logs for details.", http.StatusBadRequest)
			if rawProviderError == "" {
				rawProviderError = "no authorization code in callback"
			}
			errCh <- fmt.Errorf("OAuth callback error from provider: %s", rawProviderError)
			return
		}

		fmt.Fprintln(w, "Authorization successful! You may close this browser tab.")
		codeCh <- code
	})

	listener, err := net.Listen("tcp", "localhost:8085")
	if err != nil {
		return nil, fmt.Errorf("starting callback listener on localhost:8085: %w", err)
	}

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      10 * time.Second,
	}

	// Run the server in the background.
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			errCh <- fmt.Errorf("callback server error: %w", serveErr)
		}
	}()

	slog.Info("opening browser for Google OAuth2 consent", "url", authURL)

	if err := openBrowser(authURL); err != nil {
		slog.Warn("could not open browser automatically", "error", err)
		fmt.Printf("Please open the following URL in your browser:\n\n%s\n\n", authURL)
	}

	// Wait for the authorization code, an error, or a timeout.
	authTimeout := 5 * time.Minute
	authTimer := time.NewTimer(authTimeout)
	defer authTimer.Stop()

	var code string
	select {
	case code = <-codeCh:
		slog.Info("received authorization code")
	case err := <-errCh:
		_ = server.Shutdown(context.Background())
		return nil, err
	case <-authTimer.C:
		_ = server.Shutdown(context.Background())
		return nil, fmt.Errorf("OAuth2 flow timed out after %s waiting for browser consent", authTimeout)
	}

	// Shut down the callback server.
	if err := server.Shutdown(context.Background()); err != nil {
		slog.Warn("error shutting down callback server", "error", err)
	}

	// Exchange the authorization code for a token.
	token, err := config.Exchange(context.Background(), code)
	if err != nil {
		return nil, fmt.Errorf("exchanging authorization code for token: %w", err)
	}

	if err := saveToken(tokenPath, token); err != nil {
		return nil, fmt.Errorf("saving token: %w", err)
	}

	return token, nil
}

// openBrowser opens the given URL in the user's default browser.
// Only HTTPS URLs are allowed for safety.
func openBrowser(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" {
		return fmt.Errorf("refusing to open non-https URL in browser")
	}

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "linux":
		cmd = exec.Command("xdg-open", rawURL)
	default:
		return fmt.Errorf("unsupported platform %s for browser open", runtime.GOOS)
	}

	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// getTokenSource returns an oauth2.TokenSource that auto-refreshes the token.
// When a new token is obtained via refresh, it is automatically saved to tokenPath.
func getTokenSource(ctx context.Context, config *oauth2.Config, tokenPath string, token *oauth2.Token) oauth2.TokenSource {
	baseSource := config.TokenSource(ctx, token)
	return &savingTokenSource{
		base:      baseSource,
		tokenPath: tokenPath,
		current:   token,
	}
}

// savingTokenSource wraps an oauth2.TokenSource and saves the token to disk
// whenever a new token is obtained (e.g., after a refresh). It is safe for
// concurrent use.
type savingTokenSource struct {
	base      oauth2.TokenSource
	tokenPath string

	mu      sync.Mutex
	current *oauth2.Token
}

// Token returns a valid token, saving it to disk if it was refreshed.
func (s *savingTokenSource) Token() (*oauth2.Token, error) {
	token, err := s.base.Token()
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// If the token changed (was refreshed), persist it.
	if token.AccessToken != s.current.AccessToken {
		slog.Info("OAuth2 token refreshed, saving to disk")
		if saveErr := saveToken(s.tokenPath, token); saveErr != nil {
			slog.Error("failed to save refreshed token", "error", saveErr)
		}
		s.current = token
	}

	return token, nil
}