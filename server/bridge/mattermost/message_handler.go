package mattermost

import (
	"fmt"
	"strings"

	mmModel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	pluginModel "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
)

// mattermostMessageHandler handles incoming messages for the Mattermost bridge
type mattermostMessageHandler struct {
	bridge *mattermostBridge
	logger logger.Logger
}

// newMessageHandler creates a new Mattermost message handler
func newMessageHandler(bridge *mattermostBridge) *mattermostMessageHandler {
	return &mattermostMessageHandler{
		bridge: bridge,
		logger: bridge.logger,
	}
}

// ProcessMessage processes an incoming message for the Mattermost bridge
func (h *mattermostMessageHandler) ProcessMessage(msg *pluginModel.DirectionalMessage) error {
	h.logger.LogDebug("Processing message for Mattermost bridge",
		"source_bridge", msg.SourceBridge,
		"direction", msg.Direction,
		"channel_id", msg.SourceChannelID)

	// Skip messages that originated from Mattermost to prevent loops
	if msg.SourceBridge == "mattermost" {
		h.logger.LogDebug("Skipping Mattermost-originated message to prevent loop")
		return nil
	}

	// For incoming messages to Mattermost, we post them to Mattermost channels
	if msg.Direction == pluginModel.DirectionIncoming {
		return h.postMessageToMattermost(msg.BridgeMessage)
	}

	h.logger.LogDebug("Ignoring outgoing message for Mattermost bridge")
	return nil
}

// CanHandleMessage determines if this handler can process the message
func (h *mattermostMessageHandler) CanHandleMessage(msg *pluginModel.BridgeMessage) bool {
	// Mattermost bridge can handle text messages that didn't originate from Mattermost
	return msg.MessageType == "text" && msg.SourceBridge != "mattermost"
}

// GetSupportedMessageTypes returns the message types this handler supports
func (h *mattermostMessageHandler) GetSupportedMessageTypes() []string {
	return []string{"text"}
}

// postMessageToMattermost posts a message to a Mattermost channel
func (h *mattermostMessageHandler) postMessageToMattermost(msg *pluginModel.BridgeMessage) error {
	if h.bridge.api == nil {
		return fmt.Errorf("mattermost API not initialized")
	}

	// Get the Mattermost channel ID from the channel mapping using the source bridge name
	channelID, err := h.bridge.GetChannelMappingForBridge(msg.SourceBridge, msg.SourceChannelID)
	if err != nil {
		return fmt.Errorf("failed to get channel mapping: %w", err)
	}
	if channelID == "" {
		// Check if the source channel ID is already a Mattermost channel ID
		channelID = msg.SourceChannelID
	}

	// Verify the channel exists
	channel, appErr := h.bridge.api.GetChannel(channelID)
	if appErr != nil {
		return fmt.Errorf("failed to get channel %s: %w", channelID, appErr)
	}
	if channel == nil {
		return fmt.Errorf("channel %s not found", channelID)
	}

	// Get or create remote user for this message
	remoteUserID, err := h.getOrCreateRemoteUser(msg)
	if err != nil {
		return fmt.Errorf("failed to get or create remote user: %w", err)
	}

	if err := h.bridge.api.InviteRemoteToChannel(channelID, msg.SourceRemoteID, remoteUserID, true); err != nil {
		h.logger.LogError(
			"Failed to invite remote user to channel",
			"channel_id", msg.SourceChannelID,
			"remote_user_id", remoteUserID,
			"source_bridge", msg.SourceBridge,
			"source_remote_id", msg.SourceRemoteID,
			"err", err.Error(),
		)
	}

	// Create the post using the remote user (no need for bridge formatting since it's posted as the actual user)
	post := &mmModel.Post{
		ChannelId: channelID,
		UserId:    remoteUserID,
		Message:   msg.Content,
		Type:      mmModel.PostTypeDefault,
		Props: map[string]any{
			"from_bridge":       msg.SourceBridge,
			"bridge_message_id": msg.MessageID,
			"bridge_timestamp":  msg.Timestamp.Unix(),
		},
	}

	// Add thread ID if present
	if msg.ThreadID != "" {
		post.RootId = msg.ThreadID
	}

	// Post the message as the plugin bot
	createdPost, appErr := h.bridge.api.CreatePost(post)
	if appErr != nil {
		return fmt.Errorf("failed to create post in channel %s: %w", channelID, appErr)
	}

	h.logger.LogDebug("Message posted to Mattermost channel",
		"channel_id", channelID,
		"post_id", createdPost.Id,
		"remote_user_id", remoteUserID,
		"source_bridge", msg.SourceBridge,
		"source_user", msg.SourceUserName,
		"content_length", len(msg.Content))

	return nil
}

// getOrCreateRemoteUser gets or creates a remote user for incoming bridge messages
func (h *mattermostMessageHandler) getOrCreateRemoteUser(msg *pluginModel.BridgeMessage) (string, error) {
	// Generate username from source info
	username := h.generateUsername(msg.SourceUserID, msg.SourceUserName, msg.SourceBridge)

	// Generate email using bridge ID
	email := fmt.Sprintf("%s@bridge.%s", username, h.bridge.bridgeID)

	// First try to find existing user by username
	if existingUser, appErr := h.bridge.api.GetUserByUsername(username); appErr == nil && existingUser != nil {
		// Check if this user has the correct RemoteId
		if existingUser.RemoteId != nil && *existingUser.RemoteId == msg.SourceRemoteID {
			h.logger.LogDebug("Found existing remote user",
				"user_id", existingUser.Id,
				"username", username,
				"source_bridge", msg.SourceBridge,
				"source_remote_id", msg.SourceRemoteID)
			return existingUser.Id, nil
		}
	}

	// Also try to find user by email
	if existingUser, appErr := h.bridge.api.GetUserByEmail(email); appErr == nil && existingUser != nil {
		// Check if this user has the correct RemoteId
		if existingUser.RemoteId != nil && *existingUser.RemoteId == msg.SourceRemoteID {
			h.logger.LogDebug("Found existing remote user by email",
				"user_id", existingUser.Id,
				"email", email,
				"source_bridge", msg.SourceBridge,
				"source_remote_id", msg.SourceRemoteID)
			return existingUser.Id, nil
		}
	}

	// User doesn't exist, create the remote user
	user := &mmModel.User{
		Username:  username,
		Email:     email,
		FirstName: msg.SourceUserName,
		Password:  mmModel.NewId(),
		RemoteId:  mmModel.NewPointer(msg.SourceRemoteID),
	}

	// Try to create the user
	createdUser, appErr := h.bridge.api.CreateUser(user)
	if appErr != nil {
		h.logger.LogError("Failed to create remote user",
			"username", username,
			"email", email,
			"source_bridge", msg.SourceBridge,
			"source_remote_id", msg.SourceRemoteID,
			"error", appErr)
		return "", fmt.Errorf("failed to create remote user: %w", appErr)
	}

	h.logger.LogInfo("Created remote user",
		"user_id", createdUser.Id,
		"username", username,
		"email", email,
		"source_bridge", msg.SourceBridge,
		"source_remote_id", msg.SourceRemoteID)

	return createdUser.Id, nil
}

// generateUsername creates a username from source information
func (h *mattermostMessageHandler) generateUsername(sourceUserID, sourceUserName, sourceBridge string) string {
	var baseUsername string

	// Prefer source user name, fallback to user ID
	if sourceUserName != "" {
		baseUsername = sourceUserName
	} else {
		baseUsername = sourceUserID
	}

	// Clean the username (remove invalid characters, make lowercase)
	baseUsername = strings.ToLower(baseUsername)
	baseUsername = strings.ReplaceAll(baseUsername, "@", "")
	baseUsername = strings.ReplaceAll(baseUsername, ".", "")
	baseUsername = strings.ReplaceAll(baseUsername, " ", "")

	// Prefix with bridge type to avoid conflicts
	return fmt.Sprintf("%s-%s", sourceBridge, baseUsername)
}

// mattermostUserResolver handles user resolution for the Mattermost bridge
type mattermostUserResolver struct {
	bridge *mattermostBridge
	logger logger.Logger
}

// newUserResolver creates a new Mattermost user resolver
func newUserResolver(bridge *mattermostBridge) *mattermostUserResolver {
	return &mattermostUserResolver{
		bridge: bridge,
		logger: bridge.logger,
	}
}

// ResolveUser converts an external user ID to an ExternalUser
func (r *mattermostUserResolver) ResolveUser(externalUserID string) (*pluginModel.ExternalUser, error) {
	r.logger.LogDebug("Resolving Mattermost user", "user_id", externalUserID)

	// For Mattermost, the external user ID is the Mattermost user ID
	user, appErr := r.bridge.api.GetUser(externalUserID)
	if appErr != nil {
		return nil, fmt.Errorf("failed to get Mattermost user: %w", appErr)
	}

	if user == nil {
		return nil, fmt.Errorf("mattermost user not found: %s", externalUserID)
	}

	return &pluginModel.ExternalUser{
		BridgeType:       "mattermost",
		ExternalUserID:   externalUserID,
		DisplayName:      r.GetDisplayName(externalUserID),
		MattermostUserID: externalUserID, // Same as external ID for Mattermost
	}, nil
}

// FormatUserMention formats a user mention for Markdown content
func (r *mattermostUserResolver) FormatUserMention(user *pluginModel.ExternalUser) string {
	// For Mattermost, use the standard @username format
	return fmt.Sprintf("@%s", user.DisplayName)
}

// GetDisplayName extracts display name from external user ID
func (r *mattermostUserResolver) GetDisplayName(externalUserID string) string {
	// Try to get the actual username from Mattermost API
	user, appErr := r.bridge.api.GetUser(externalUserID)
	if appErr != nil || user == nil {
		r.logger.LogWarn("Failed to get user for display name", "user_id", externalUserID)
		return "Unknown User"
	}

	// Prefer username, fallback to first name + last name, then to ID
	if user.Username != "" {
		return user.Username
	}

	fullName := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if fullName != "" {
		return fullName
	}

	return user.Id[:8] // Show first 8 chars of ID as fallback
}
