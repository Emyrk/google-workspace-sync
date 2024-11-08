# google-workspace-sync

This script will sync users from Google Workspace Groups to Coder Groups. It
does the following:

```
1. Fetchs all users from your Coder deployment
2. For each user
  2a. Fetch the user's Coder groups
  2b. Fetch the user's Google Workspace groups
  2c. Creates any groups that do not exist in Coder yet
  2d. Adds & removes users from Coder groups to match Google Workspace groups
    - Groups are matched by name. Lowercased and spaces removed.
    - coderGroupName = strings.Replace(lower(googleGroupName), " ", "")
3. Logs all changes made
```

This scripts only affects the default organization.

And example run of the script where the user `alice` is moved from `catlovers` to
`doglovers`. And the group does not yet exist.

```
2024/11/08 11:29:27 Changes made:
2024/11/08 11:29:27 Created 1 groups
2024/11/08 11:29:27     doglovers :: bbd439fb-c781-409e-8e82-9b334b2b2d83
2024/11/08 11:29:27 Changes to group memberships:
2024/11/08 11:29:27     Group doglovers: 1 added, 0 removed
2024/11/08 11:29:27             Added: [alice@example.com]
2024/11/08 11:29:27             Removed: []
2024/11/08 11:29:27     Group catlovers: 0 added, 1 removed
2024/11/08 11:29:27             Added: []
2024/11/08 11:29:27             Removed: [alice@example.com]
```

When no changes have occurred
```
2024/11/08 11:30:27 No changes to make, all 1 OIDC users in your Google domain are in the correct groups
```

# To deploy in GCloud function

Only the code in `main.go` needs to be copied into a google cloud run function. All the logic is contained in `gcsync` package.

To control the code yourself, you can fork this repository and run this command to change the `go.mod` to point to your new repo:

```
go mod edit -replace github.com/Emyrk/google-workspace-sync=github.com/<YOUR_FORK>/google-workspace-sync@main
```

Resulting in this `go.mod` entry:

```
replace github.com/Emyrk/google-workspace-sync => github.com/<YOUR_FORK>/google-workspace-sync main
```