package mattermost

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	pluginModel "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
	mmModel "github.com/mattermost/mattermost/server/public/model"
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
		return fmt.Errorf("Mattermost API not initialized")
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

	// Format the message content
	content := h.formatMessageContent(msg)

	// Create the post
	post := &mmModel.Post{
		ChannelId: channelID,
		UserId:    h.bridge.botUserID,
		Message:   content,
		Type:      mmModel.PostTypeDefault,
		Props: map[string]interface{}{
			"from_bridge":       msg.SourceBridge,
			"bridge_user_id":    msg.SourceUserID,
			"bridge_user_name":  msg.SourceUserName,
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
		"source_bridge", msg.SourceBridge,
		"content_length", len(content))

	return nil
}

// formatMessageContent formats the message content for Mattermost
func (h *mattermostMessageHandler) formatMessageContent(msg *pluginModel.BridgeMessage) string {
	// For messages from other bridges, prefix with the bridge info and user name
	if msg.SourceUserName != "" {
		bridgeIcon := h.getBridgeIcon(msg.SourceBridge)
		return fmt.Sprintf("%s **%s**: %s", bridgeIcon, msg.SourceUserName, msg.Content)
	}
	return msg.Content
}

// getBridgeIcon returns an icon/emoji for the source bridge
func (h *mattermostMessageHandler) getBridgeIcon(bridgeType string) string {
	switch bridgeType {
	case "xmpp":
		return ":speech_balloon:" // Chat bubble emoji for XMPP
	case "slack":
		return ":slack:" // Slack emoji if available
	case "discord":
		return ":discord:" // Discord emoji if available
	default:
		return ":bridge_at_night:" // Generic bridge emoji
	}
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
		return nil, fmt.Errorf("Mattermost user not found: %s", externalUserID)
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
