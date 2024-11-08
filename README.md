# google-workspace-sync

This script will sync users from Google Workspace Groups to Coder Groups.

All groups are matched by name. Google Workspace group names are lowercase'd and
spaces are removed. If a group name is still invalid, it will be skipped.

`coderGroupName = strings.Replace(lower(googleGroupName), " ", "")`


This scripts only affects the default organization.

Groups are automatically created in Coder if they do not exist.

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