package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/oauth2/google"
	admin "google.golang.org/api/admin/directory/v1"
	"google.golang.org/api/option"

	"github.com/coder/coder/v2/coderd/util/slice"
	"github.com/coder/coder/v2/codersdk"
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
	credJSON, err := os.ReadFile(credentialsJSONFilePath)
	if err != nil {
		log.Fatalf("failed to read credentials from %q: %v", credentialsJSONFilePath, err)
	}

	// Auth with Google using delegated credentials
	cjwt, err := google.JWTConfigFromJSON(credJSON, admin.CloudPlatformScope,
		admin.AdminDirectoryUserScope,
		admin.AdminDirectoryUserReadonlyScope,
		admin.AdminDirectoryGroupScope,
		admin.AdminDirectoryGroupReadonlyScope)
	if err != nil {
		log.Fatalf("failed to create JWT config from JSON (bytes=%d): %v", len(credJSON), err)
	}
	// Set to your google workspace admin user.
	// https://developers.google.com/identity/protocols/oauth2/service-account#delegatingauthority
	cjwt.Subject = delegatedUserEmail

	// Authenticate with Google
	adminService, err := admin.NewService(ctx,
		option.WithHTTPClient(cjwt.Client(ctx)),
	)
	if err != nil {
		panic(err)
	}

	// Authenticate with Coder
	u, _ := url.Parse(coderURL)
	client := codersdk.New(u)
	client.SetSessionToken(coderSessionToken)

	coderOrganizations, err := client.Organizations(ctx)
	if err != nil {
		log.Fatalf("failed to get coder organizations: %v", err)
	}

	// Only syncing groups into the default organization
	var defaultOrg codersdk.Organization
	for _, org := range coderOrganizations {
		if org.IsDefault {
			defaultOrg = org
			break
		}
	}

	userByID := make(map[uuid.UUID]codersdk.User) // Used for logging/debugging
	coderGroups := make(map[string]codersdk.Group)
	coderGroupChanges := make(ChangeGroupRequests)
	coderGroupsResp, err := client.Groups(ctx, codersdk.GroupArguments{
		Organization: defaultOrg.ID.String(),
	})
	if err != nil {
		log.Fatalf("failed to get coder groups: %v", err)
	}

	for _, group := range coderGroupsResp {
		coderGroups[group.Name] = group
	}

	// Find all users on Coder
	coderUsers, err := client.Users(ctx, codersdk.UsersRequest{})
	if err != nil {
		log.Fatalf("failed to get coder users: %w", err)
	}

	oidcUserCount := 0
	for _, user := range coderUsers.Users {
		userByID[user.ID] = user
		if user.LoginType != codersdk.LoginTypeOIDC {
			continue // Not a Google Workspace user
		}

		// Only for the Google Workspace domain
		if !strings.HasSuffix(user.Email, "@"+googleWorkspaceDomain) {
			continue
		}
		oidcUserCount++

		gGroups, err := GoogleGroups(ctx, adminService, user.Email)
		if err != nil {
			log.Fatalf("failed to get Google Groups for %s: %v", user.Email, err)
		}

		cGroups, err := client.Groups(ctx, codersdk.GroupArguments{
			HasMember: user.Username,
		})
		if err != nil {
			log.Fatalf("failed to get coder groups for user %s: %v", user.Email, err)
		}

		var everyoneGroup = "Everyone"
		var cGroupNames []string
		for _, group := range cGroups {
			cGroupNames = append(cGroupNames, group.Name)
			if group.ID == defaultOrg.ID {
				everyoneGroup = group.Name
			}
		}

		expected := ExpectedCoderGroups(gGroups)
		// expected is the set of groups the user should be in
		// SymmetricDifference returns the groups to add & remove
		// to make the set {cGroupNames} match the set {expected}
		add, remove := slice.SymmetricDifference(cGroupNames, append(expected, everyoneGroup))
		for _, group := range add {
			coderGroupChanges.AddUser(group, user.ID.String())
		}
		for _, group := range remove {
			coderGroupChanges.RemoveUser(group, user.ID.String())
		}
	}

	var createdGroups []codersdk.Group
	// Create missing groups
	for group, _ := range coderGroupChanges {
		if _, ok := coderGroups[group]; !ok {
			newGroup, err := client.CreateGroup(ctx, defaultOrg.ID, codersdk.CreateGroupRequest{
				Name:        group,
				DisplayName: "",
				// The "NEW" icon
				AvatarURL:      "/emojis/1f195.png",
				QuotaAllowance: 0,
			})
			if err != nil {
				delete(coderGroupChanges, group)
				log.Printf("failed to create group %q, users in this group will not be assigned: %v", group, err)
				continue
			}
			createdGroups = append(createdGroups, newGroup)
			coderGroups[group] = newGroup
		}
	}

	if len(coderGroupChanges) == 0 {
		log.Printf("No changes to make, all %d OIDC users in your Google domain are in the correct groups", oidcUserCount)
		return
	}

	log.Println("Changes made:")
	if len(createdGroups) > 0 {
		log.Printf("Created %d groups", len(createdGroups))
		for _, group := range createdGroups {
			log.Printf("\t%s :: %s", group.Name, group.ID)
		}
	}

	if len(coderGroupChanges) > 0 {
		log.Printf("Changes to group memberships:")
	}

	// Add/Remove all the users
	for group, req := range coderGroupChanges {
		coderGroup, ok := coderGroups[group]
		if !ok {
			log.Fatalf("group %s not found, does it exist in Coder?", group)
		}

		_, err = client.PatchGroup(ctx, coderGroup.ID, req)
		if err != nil {
			log.Fatalf("failed to patch group %s: %v", group, err)
		}

		log.Printf("\tGroup %s: %d added, %d removed", group, len(req.AddUsers), len(req.RemoveUsers))
		log.Printf("\t\tAdded: %v", UserIDsToNames(userByID, req.AddUsers))
		log.Printf("\t\tRemoved: %v", UserIDsToNames(userByID, req.RemoveUsers))
	}
}

// ExpectedCoderGroups returns the list of group names the user is expected
// to be in based on the Google Groups they are in.
func ExpectedCoderGroups(groups []*admin.Group) []string {
	var expected []string
	for _, group := range groups {
		if group.Name == "" {
			log.Printf("Google Group %s has no groupname, skipping", group.Email)
			continue
		}

		// normalize names to lowercase and remove spaces
		normalizedName := strings.ToLower(strings.ReplaceAll(group.Name, " ", ""))
		expected = append(expected, normalizedName)
	}
	return expected
}

func GoogleGroups(ctx context.Context, srv *admin.Service, email string) ([]*admin.Group, error) {
	var allGroups []*admin.Group
	var pageToken string

	// Call api until all groups are read. Loop for pagination
	for {
		googleGroups, err := srv.Groups.List().
			Context(ctx).
			PageToken(pageToken).
			UserKey(email).
			Do()
		if err != nil {
			return nil, fmt.Errorf("failed to list groups: %w", err)
		}

		allGroups = append(allGroups, googleGroups.Groups...)
		if googleGroups.NextPageToken == "" {
			break
		}
		pageToken = googleGroups.NextPageToken
	}

	return allGroups, nil
}

func GoogleUsers(ctx context.Context, srv *admin.Service) ([]*admin.User, error) {
	var allUsers []*admin.User
	var pageToken string

	// Call api until all users are read. Loop for pagination
	for {
		googleUsers, err := srv.Users.List().
			// Customer ID: https://support.google.com/a/answer/10070793?hl=en
			Customer(googleCustomerID).
			Context(ctx).
			PageToken(pageToken).
			Do()
		if err != nil {
			return nil, fmt.Errorf("failed to list users: %w", err)
		}

		allUsers = append(allUsers, googleUsers.Users...)
		if googleUsers.NextPageToken == "" {
			break
		}
		pageToken = googleUsers.NextPageToken
	}

	return allUsers, nil
}

func UserIDsToNames(lookup map[uuid.UUID]codersdk.User, ids []string) []string {
	var names []string
	for _, idStr := range ids {
		id, err := uuid.Parse(idStr)
		if user, ok := lookup[id]; err == nil && ok {
			names = append(names, user.Email)
			continue
		}
		names = append(names, id.String())
	}
	return names
}

type ChangeGroupRequests map[string]codersdk.PatchGroupRequest

func (c ChangeGroupRequests) AddUser(group, user string) {
	if _, ok := c[group]; !ok {
		c[group] = codersdk.PatchGroupRequest{}
	}
	req := c[group]
	req.AddUsers = append(req.AddUsers, user)
	c[group] = req
}

func (c ChangeGroupRequests) RemoveUser(group, user string) {
	if _, ok := c[group]; !ok {
		c[group] = codersdk.PatchGroupRequest{}
	}
	req := c[group]
	req.RemoveUsers = append(req.RemoveUsers, user)
	c[group] = req
}
