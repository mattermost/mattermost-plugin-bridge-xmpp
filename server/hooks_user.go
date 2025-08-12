package main

import (
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// UserHasBeenDeleted is called when a user has been deleted from Mattermost
// This allows us to clean up ghost users from the XMPP server
func (p *Plugin) UserHasBeenDeleted(c *plugin.Context, user *model.User) {
	if user == nil {
		p.logger.LogWarn("UserHasBeenDeleted called with nil user")
		return
	}

	p.logger.LogDebug("User deleted from Mattermost, cleaning up bridge users", "user_id", user.Id, "username", user.Username)

	// Clean up ghost users from external bridges (skip Mattermost bridge)
	for _, bridgeName := range p.bridgeManager.ListBridges() {
		// Skip the Mattermost bridge since it represents the Mattermost side
		if bridgeName == "mattermost" {
			p.logger.LogDebug("Skipping Mattermost bridge for user cleanup", "user_id", user.Id)
			continue
		}

		bridge, err := p.bridgeManager.GetBridge(bridgeName)
		if err != nil {
			p.logger.LogWarn("Failed to get bridge for user cleanup", "bridge", bridgeName, "error", err)
			continue
		}

		userManager := bridge.GetUserManager()
		if userManager == nil {
			p.logger.LogDebug("Bridge has no user manager, skipping cleanup", "bridge", bridgeName)
			continue
		}

		// Check if this user exists in the bridge
		if !userManager.HasUser(user.Id) {
			p.logger.LogDebug("User not found in bridge, skipping cleanup", "bridge", bridgeName, "user_id", user.Id)
			continue
		}

		// Delete the user from the bridge (this will handle ghost user cleanup if enabled)
		if err := userManager.DeleteUser(user.Id); err != nil {
			p.logger.LogWarn("Failed to delete user from bridge", "bridge", bridgeName, "user_id", user.Id, "error", err)
		} else {
			p.logger.LogInfo("Successfully deleted user from bridge", "bridge", bridgeName, "user_id", user.Id, "username", user.Username)
		}
	}
}
