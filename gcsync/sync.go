package gcsync

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/oauth2/google"
	admin "google.golang.org/api/admin/directory/v1"
	"google.golang.org/api/option"

	"github.com/coder/coder/v2/coderd/util/slice"
	"github.com/coder/coder/v2/codersdk"
)

type Sync struct {
	CoderClient  *codersdk.Client
	GoogleClient *admin.Service
	EmailDomain  string
}

type Config struct {
	// CoderURL should be the access url of the Coder deployment.
	CoderURL string
	// CoderSessionToken should be the session token of an owner Coder user.
	CoderSessionToken string

	// GoogleCustomerID should be the Google Workspace customer ID.
	// From https://support.google.com/a/answer/10070793?hl=en
	GoogleCustomerID string
	// GCPCredentialsJSONFilePath should be a service account json file
	// with credentials.
	GCPCredentialsJSONFilePath string
	// DelegatedUserEmail should be the email of the Google Workspace admin
	// https://developers.google.com/identity/protocols/oauth2/service-account#delegatingauthority
	DelegatedUserEmail string
	// EmailDomain should be the domain of the Google Workspace.
	// This is used to ignore users that are not in Google Workspace.
	EmailDomain string
}

func New(ctx context.Context, cfg *Config) (*Sync, error) {
	// Coder client
	u, _ := url.Parse(cfg.CoderURL)
	client := codersdk.New(u)
	client.SetSessionToken(cfg.CoderSessionToken)
	_, err := client.User(ctx, codersdk.Me)
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate with coder: %w", err)
	}

	// Google Workspace Admin SDK Client
	credJSON, err := os.ReadFile(cfg.GCPCredentialsJSONFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read credentials from %q: %v", cfg.GCPCredentialsJSONFilePath, err)
	}

	// Auth with Google using delegated credentials
	cjwt, err := google.JWTConfigFromJSON(credJSON, admin.CloudPlatformScope,
		admin.AdminDirectoryUserScope,
		admin.AdminDirectoryUserReadonlyScope,
		admin.AdminDirectoryGroupScope,
		admin.AdminDirectoryGroupReadonlyScope)
	if err != nil {
		return nil, fmt.Errorf("failed to create JWT config from JSON (bytes=%d): %v", len(credJSON), err)
	}
	cjwt.Subject = cfg.DelegatedUserEmail

	// Authenticate with Google
	adminService, err := admin.NewService(ctx,
		option.WithHTTPClient(cjwt.Client(ctx)),
	)
	if err != nil {
		return nil, fmt.Errorf("authenticate google: %w", err)
	}

	return &Sync{
		CoderClient:  client,
		GoogleClient: adminService,
		EmailDomain:  cfg.EmailDomain,
	}, nil
}

func (s *Sync) SyncGroups(ctx context.Context) error {
	defaultOrg, err := s.defaultOrganization(ctx)
	if err != nil {
		return err
	}

	coderGroups, err := s.coderGroups(ctx, defaultOrg.ID)
	if err != nil {
		return err
	}

	userByID := make(map[uuid.UUID]codersdk.User) // Used for logging/debugging
	coderGroupChanges := make(ChangeGroupRequests)

	// Find all users on Coder
	coderUsers, err := s.CoderClient.Users(ctx, codersdk.UsersRequest{})
	if err != nil {
		return fmt.Errorf("failed to get coder users: %w", err)
	}

	oidcUserCount := 0
	// For each user, find the groups they are in on Google Workspace
	// and on Coder. Then calculate the changes needed to sync the groups.
	for _, user := range coderUsers.Users {
		userByID[user.ID] = user
		if user.LoginType != codersdk.LoginTypeOIDC {
			continue // Not a Google Workspace user
		}

		// Only for the Google Workspace domain
		if !strings.HasSuffix(user.Email, "@"+s.EmailDomain) {
			continue
		}
		oidcUserCount++

		gGroups, err := GoogleGroups(ctx, s.GoogleClient, user.Email)
		if err != nil {
			log.Fatalf("failed to get Google Groups for %s: %v", user.Email, err)
		}

		cGroups, err := s.CoderClient.Groups(ctx, codersdk.GroupArguments{
			HasMember: user.Username,
		})
		if err != nil {
			log.Fatalf("failed to get coder groups for user %s: %v", user.Email, err)
		}

		// Everyone group should include everyone, so always include it.
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

	if len(coderGroupChanges) == 0 {
		log.Printf("No changes to make, all %d OIDC users in your Google domain are in the correct groups", oidcUserCount)
		return nil
	}

	// Create missing groups and update our coderGroups map
	coderGroups, coderGroupChanges, err = s.createMissingGroups(ctx, defaultOrg.ID, coderGroups, coderGroupChanges)
	if err != nil {
		return fmt.Errorf("failed to create missing groups: %w", err)
	}

	// Apply the changes to the groups
	err = s.applyGroupChanges(ctx, defaultOrg.ID, userByID, coderGroups, coderGroupChanges)
	if err != nil {
		return fmt.Errorf("failed to apply group changes: %w", err)
	}

	return nil
}

func (s *Sync) applyGroupChanges(ctx context.Context, org uuid.UUID, lookupUser map[uuid.UUID]codersdk.User, coderGroups map[string]codersdk.Group, changes ChangeGroupRequests) error {
	// Add/Remove all the users
	for group, req := range changes {
		coderGroup, ok := coderGroups[group]
		if !ok {
			return fmt.Errorf("group %s not found, unable to apply group sync", group)
		}

		_, err := s.CoderClient.PatchGroup(ctx, coderGroup.ID, req)
		if err != nil {
			return fmt.Errorf("failed to patch group %s: %w", group, err)
		}

		log.Printf("\tGroup %s: %d added, %d removed", group, len(req.AddUsers), len(req.RemoveUsers))
		log.Printf("\t\tAdded: %v", UserIDsToNames(lookupUser, req.AddUsers))
		log.Printf("\t\tRemoved: %v", UserIDsToNames(lookupUser, req.RemoveUsers))
	}
	return nil
}

func (s *Sync) createMissingGroups(ctx context.Context, org uuid.UUID, coderGroups map[string]codersdk.Group, changes ChangeGroupRequests) (map[string]codersdk.Group, ChangeGroupRequests, error) {
	var createdGroups []codersdk.Group
	for group, _ := range changes {
		if _, ok := coderGroups[group]; !ok {
			// Group is missing and must be created.
			newGroup, err := s.CoderClient.CreateGroup(ctx, org, codersdk.CreateGroupRequest{
				Name:        group,
				DisplayName: "",
				// The "NEW" icon
				AvatarURL:      "/emojis/1f195.png",
				QuotaAllowance: 0,
			})
			if err != nil {
				delete(changes, group)
				log.Printf("failed to create group %q, users in this group will not be assigned: %v", group, err)
				continue
			}
			coderGroups[group] = newGroup
			createdGroups = append(createdGroups, newGroup)
		}
	}

	if len(createdGroups) > 0 {
		log.Printf("Created %d groups", len(createdGroups))
		for _, group := range createdGroups {
			log.Printf("\t%s :: %s", group.Name, group.ID)
		}
	}

	return coderGroups, changes, nil
}

func (s *Sync) userChanges(ctx context.Context, user codersdk.User) (add []string, remove []string, err error) {
	if user.LoginType != codersdk.LoginTypeOIDC {
		return []string{}, []string{}, nil
	}

	if !strings.HasSuffix(user.Email, "@"+s.EmailDomain) {
		return []string{}, []string{}, nil
	}

	return nil, nil, nil
}

func (s *Sync) coderGroups(ctx context.Context, orgID uuid.UUID) (map[string]codersdk.Group, error) {
	coderGroupsResp, err := s.CoderClient.Groups(ctx, codersdk.GroupArguments{
		Organization: orgID.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get coder groups: %w", err)
	}

	groupMap := make(map[string]codersdk.Group)
	for _, group := range coderGroupsResp {
		groupMap[group.Name] = group
	}
	return groupMap, nil
}

func (s *Sync) defaultOrganization(ctx context.Context) (codersdk.Organization, error) {
	coderOrganizations, err := s.CoderClient.Organizations(ctx)
	if err != nil {
		return codersdk.Organization{}, fmt.Errorf("failed to get coder organizations: %w", err)
	}

	for _, org := range coderOrganizations {
		if org.IsDefault {
			return org, nil
		}
	}
	return codersdk.Organization{}, fmt.Errorf("default organization not found")
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
