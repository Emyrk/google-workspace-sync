package gcsync

import "github.com/coder/coder/v2/codersdk"

// ChangeGroupRequests stores all the mutations to be made to groups.
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
