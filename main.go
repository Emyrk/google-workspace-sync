package main

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/Emyrk/google-workspace-sync/gcsync"
)

// Requirements:
// 1. Google Workspace Admin SDK enabled
// 2. Service account with domain-wide delegation
// https://developers.google.com/identity/protocols/oauth2/service-account#delegatingauthority
// 3. Service Account
// 4. Rewrite `ExpectedCoderGroups` to your own groups.
var (
	// delegatedUserEmail must be an admin of the Google Workspaces
	delegatedUserEmail      = takeEnvVar("CODER_G_ADMIN_EMAIL", "alice@example.com")
	googleWorkspaceDomain   = takeEnvVar("CODER_G_SYNC_DOMAIN", "example.com")
	homeDir, _              = os.UserHomeDir()
	credentialsJSONFilePath = takeEnvVar("CODER_G_SYNC_CREDS_FILEPATH", filepath.Join(homeDir, "coder", "google-credentials.json"))
	coderURL                = takeEnvVar("CODER_G_SYNC_CODER_URL", "https://coder.example.com")
	// coderSessionToken should be from an owner account.
	coderSessionToken = takeEnvVar("CODER_G_SYNC_SESSION_TOKEN", "APM...w")
	// googleCustomerID get from https://support.google.com/a/answer/10070793?hl=en
	googleCustomerID = takeEnvVar("CODER_G_SYNC_CUSTOMER_ID", "G25a24h2h")
)

func takeEnvVar(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// Fetches all users currently in Coder.
// Using their email address, will find all Google Groups they are in.
// If they are in a Google Group that corresponds to a Coder Group, they will be added to that group.
// If they are in a Coder Group that does not correspond to a Google Group, they will be removed from that group.
// Groups are matched by name. Names are mutated to be lowercase and have spaces removed.
// If the group does not exist in Coder, it will be created.
func main() {
	ctx := context.Background()
	s, err := gcsync.New(ctx, &gcsync.Config{
		CoderURL:                   coderURL,
		CoderSessionToken:          coderSessionToken,
		GoogleCustomerID:           googleCustomerID,
		GCPCredentialsJSONFilePath: credentialsJSONFilePath,
		DelegatedUserEmail:         delegatedUserEmail,
		EmailDomain:                googleWorkspaceDomain,
	})
	if err != nil {
		log.Fatalf("failed to create sync: %v", err)
	}

	err = s.SyncGroups(ctx)
	if err != nil {
		log.Fatalf("failed to sync groups: %v", err)
	}
}
