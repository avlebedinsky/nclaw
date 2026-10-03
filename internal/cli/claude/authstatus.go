package claude

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"time"
)

var authFailureMarkers = []string{
	"Failed to authenticate",
	"OAuth session expired",
	"OAuth token has expired",
	"OAuth token revoked",
	"Invalid API key",
	"authentication_error",
}

// AuthExpiry returns when the claude.ai sign-in stored in the credentials file ends.
// That is the refresh token's expiry: refreshing the access token does not extend it.
// It is unknown when the CLI takes its credentials from the environment.
func (p *Provider) AuthExpiry() (time.Time, bool) {
	if credentialsFromEnv() {
		return time.Time{}, false
	}

	refreshMu.Lock()
	defer refreshMu.Unlock()

	path, err := credPathFunc()
	if err != nil {
		log.Printf("claude/auth: credentials path: %v", err)
		return time.Time{}, false
	}
	expiry, err := readSessionExpiry(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("claude/auth: read sign-in expiry: %v", err)
		}
		return time.Time{}, false
	}
	return expiry, !expiry.IsZero()
}

func readSessionExpiry(path string) (time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}
	var creds struct {
		ClaudeAiOauth struct {
			RefreshTokenExpiresAt int64 `json:"refreshTokenExpiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return time.Time{}, err
	}
	if creds.ClaudeAiOauth.RefreshTokenExpiresAt == 0 {
		return time.Time{}, nil
	}
	return time.UnixMilli(creds.ClaudeAiOauth.RefreshTokenExpiresAt), nil
}

// IsAuthFailure reports whether the output of a failed run is the CLI's sign-in error.
func (p *Provider) IsAuthFailure(output string) bool {
	for _, marker := range authFailureMarkers {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}
