package drive

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestLoadToken_NonExistent(t *testing.T) {
	token, err := loadToken("/tmp/this-path-does-not-exist/token.json")
	if err != nil {
		t.Fatalf("expected nil error for nonexistent file, got: %v", err)
	}
	if token != nil {
		t.Fatalf("expected nil token for nonexistent file, got: %+v", token)
	}
}

func TestSaveAndLoadToken(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")

	expiry := time.Now().Add(time.Hour).Truncate(time.Second)
	saved := &oauth2.Token{
		AccessToken:  "ya29.test-access-token",
		TokenType:    "Bearer",
		RefreshToken: "1//test-refresh-token",
		Expiry:       expiry,
	}

	if err := saveToken(tokenPath, saved); err != nil {
		t.Fatalf("saveToken failed: %v", err)
	}

	loaded, err := loadToken(tokenPath)
	if err != nil {
		t.Fatalf("loadToken failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("loadToken returned nil token")
	}

	if loaded.AccessToken != saved.AccessToken {
		t.Errorf("AccessToken mismatch: got %q, want %q", loaded.AccessToken, saved.AccessToken)
	}
	if loaded.TokenType != saved.TokenType {
		t.Errorf("TokenType mismatch: got %q, want %q", loaded.TokenType, saved.TokenType)
	}
	if loaded.RefreshToken != saved.RefreshToken {
		t.Errorf("RefreshToken mismatch: got %q, want %q", loaded.RefreshToken, saved.RefreshToken)
	}
	if !loaded.Expiry.Equal(saved.Expiry) {
		t.Errorf("Expiry mismatch: got %v, want %v", loaded.Expiry, saved.Expiry)
	}
}

func TestSaveToken_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "nested", "deep", "token.json")

	token := &oauth2.Token{
		AccessToken:  "ya29.test-access-token",
		TokenType:    "Bearer",
		RefreshToken: "1//test-refresh-token",
		Expiry:       time.Now().Add(time.Hour),
	}

	if err := saveToken(tokenPath, token); err != nil {
		t.Fatalf("saveToken failed: %v", err)
	}

	if _, err := os.Stat(tokenPath); err != nil {
		t.Fatalf("expected token file to exist at %s, got error: %v", tokenPath, err)
	}
}

func TestSaveToken_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")

	token := &oauth2.Token{
		AccessToken:  "ya29.test-access-token",
		TokenType:    "Bearer",
		RefreshToken: "1//test-refresh-token",
		Expiry:       time.Now().Add(time.Hour),
	}

	if err := saveToken(tokenPath, token); err != nil {
		t.Fatalf("saveToken failed: %v", err)
	}

	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("os.Stat failed: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected file permissions 0600, got %04o", perm)
	}
}

func TestLoadToken_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")

	if err := os.WriteFile(tokenPath, []byte("this is not valid json{{{"), 0600); err != nil {
		t.Fatalf("failed to write invalid JSON file: %v", err)
	}

	token, err := loadToken(tokenPath)
	if err == nil {
		t.Fatalf("expected error for invalid JSON, got nil (token: %+v)", token)
	}
	if token != nil {
		t.Errorf("expected nil token on error, got: %+v", token)
	}
}

func TestLoadOAuthConfig_InvalidPath(t *testing.T) {
	_, err := loadOAuthConfig("/tmp/nonexistent-credentials-file.json")
	if err == nil {
		t.Fatal("expected error for nonexistent credentials file, got nil")
	}
}
