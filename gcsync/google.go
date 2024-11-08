package gcsync

import (
	"context"
	"fmt"

	admin "google.golang.org/api/admin/directory/v1"
)

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

func GoogleUsers(ctx context.Context, customerID string, srv *admin.Service) ([]*admin.User, error) {
	var allUsers []*admin.User
	var pageToken string

	// Call api until all users are read. Loop for pagination
	for {
		googleUsers, err := srv.Users.List().
			// Customer ID: https://support.google.com/a/answer/10070793?hl=en
			Customer(customerID).
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
