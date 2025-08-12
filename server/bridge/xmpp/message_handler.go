package xmpp

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	pluginModel "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
	xmppClient "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/xmpp"
)

// xmppMessageHandler handles incoming messages for the XMPP bridge
type xmppMessageHandler struct {
	bridge *xmppBridge
	logger logger.Logger
}

// newMessageHandler creates a new XMPP message handler
func newMessageHandler(bridge *xmppBridge) *xmppMessageHandler {
	return &xmppMessageHandler{
		bridge: bridge,
		logger: bridge.logger,
	}
}

// ProcessMessage processes an incoming message for the XMPP bridge
func (h *xmppMessageHandler) ProcessMessage(msg *pluginModel.DirectionalMessage) error {
	h.logger.LogDebug("Processing message for XMPP bridge",
		"source_bridge", msg.SourceBridge,
		"direction", msg.Direction,
		"channel_id", msg.SourceChannelID)

	// Skip messages that originated from XMPP to prevent loops
	if msg.SourceBridge == "xmpp" {
		h.logger.LogDebug("Skipping XMPP-originated message to prevent loop")
		return nil
	}

	// For incoming messages to XMPP, we send them to XMPP rooms
	if msg.Direction == pluginModel.DirectionOutgoing {
		return h.sendMessageToXMPP(msg.BridgeMessage)
	}

	h.logger.LogDebug("Ignoring outgoing message for XMPP bridge")
	return nil
}

// CanHandleMessage determines if this handler can process the message
func (h *xmppMessageHandler) CanHandleMessage(msg *pluginModel.BridgeMessage) bool {
	// XMPP bridge can handle text messages that didn't originate from XMPP
	return msg.MessageType == "text" && msg.SourceBridge != "xmpp"
}

// GetSupportedMessageTypes returns the message types this handler supports
func (h *xmppMessageHandler) GetSupportedMessageTypes() []string {
	return []string{"text"}
}

// sendMessageToXMPP sends a message to an XMPP room
func (h *xmppMessageHandler) sendMessageToXMPP(msg *pluginModel.BridgeMessage) error {
	// Get the XMPP room JID from the channel mapping
	roomJID, err := h.bridge.GetChannelMapping(msg.SourceChannelID)
	if err != nil {
		return fmt.Errorf("failed to get room mapping: %w", err)
	}
	if roomJID == "" {
		return fmt.Errorf("channel is not mapped to any XMPP room")
	}

	// Check if we're using ghost users (XMPPUserManager)
	userManager := h.bridge.userManager
	if userManager == nil {
		return fmt.Errorf("user manager not available")
	}

	h.logger.LogDebug("Routing message", "manager_type", fmt.Sprintf("%T", userManager), "source_user_id", msg.SourceUserID, "room_jid", roomJID)

	// Check if this is an XMPPUserManager (ghost users enabled)
	if xmppUserManager, ok := userManager.(*UserManager); ok {
		// Ghost users are enabled - send message through ghost user's own client
		return h.sendMessageViaGhostUser(xmppUserManager, msg, roomJID)
	}

	// Regular user manager - send message through bridge client
	return h.sendMessageViaBridgeUser(msg, roomJID)
}

// sendMessageViaGhostUser sends a message using a ghost user's individual XMPP client
func (h *xmppMessageHandler) sendMessageViaGhostUser(xmppUserManager *UserManager, msg *pluginModel.BridgeMessage, roomJID string) error {
	// Validate source user ID for ghost user messaging
	if msg.SourceUserID == "" {
		return fmt.Errorf("cannot send message with ghost users: source user ID is empty")
	}

	// Get or create the ghost user
	bridgeUser, err := xmppUserManager.GetOrCreateUser(msg.SourceUserID, msg.SourceUserName)
	if err != nil {
		h.logger.LogWarn("Failed to get/create ghost user, falling back to bridge user", "source_user_id", msg.SourceUserID, "error", err)
		return h.sendMessageViaBridgeUser(msg, roomJID)
	}

	// Cast to XMPPUser to access XMPP-specific methods
	xmppUser, ok := bridgeUser.(*User)
	if !ok {
		return fmt.Errorf("expected XMPPUser, got %T", bridgeUser)
	}

	// Check if the ghost user is connected
	if !xmppUser.IsConnected() {
		h.logger.LogDebug("Ghost user not connected, attempting to connect", "user_id", msg.SourceUserID, "ghost_jid", xmppUser.GetJID())
		// TODO: Start the user if not started - this will be handled in the user manager fix
		h.logger.LogWarn("Ghost user not connected, falling back to bridge user", "user_id", msg.SourceUserID, "ghost_jid", xmppUser.GetJID())
		return h.sendMessageViaBridgeUser(msg, roomJID)
	}

	// Format message content for ghost user (no prefix needed since it's sent as the actual user)
	content := msg.Content

	// Send message through the ghost user's own XMPP client
	err = xmppUser.SendMessageToChannel(roomJID, content)
	if err != nil {
		h.logger.LogWarn("Failed to send message via ghost user, falling back to bridge user", "user_id", msg.SourceUserID, "ghost_jid", xmppUser.GetJID(), "error", err)
		return h.sendMessageViaBridgeUser(msg, roomJID)
	}

	// Update user activity in KV store after successful message send
	if err := xmppUserManager.UpdateUserActivity(msg.SourceUserID); err != nil {
		h.logger.LogError("Failed to update user activity after message send, user may never disconnect", "user_id", msg.SourceUserID, "error", err)
		// Don't fail the message send for activity update failures
	}

	h.logger.LogDebug("Message sent via ghost user",
		"source_user_id", msg.SourceUserID,
		"ghost_jid", xmppUser.GetJID(),
		"room_jid", roomJID,
		"content_length", len(content))

	return nil
}

// sendMessageViaBridgeUser sends a message using the bridge user's XMPP client
func (h *xmppMessageHandler) sendMessageViaBridgeUser(msg *pluginModel.BridgeMessage, roomJID string) error {
	// Validate bridge client is available
	if h.bridge.bridgeClient == nil {
		return fmt.Errorf("XMPP bridge client not initialized")
	}

	if !h.bridge.connected.Load() {
		return fmt.Errorf("not connected to XMPP server")
	}

	// Get bridge user JID
	h.bridge.configMu.RLock()
	bridgeJID := h.bridge.config.XMPPUsername
	h.bridge.configMu.RUnlock()

	// Format message content for bridge user (prefix with original username)
	content := h.formatMessageContentForBridgeUser(msg)

	// Create XMPP message request
	req := xmppClient.MessageRequest{
		RoomJID:      roomJID,
		GhostUserJID: bridgeJID,
		Message:      content,
	}

	// Send the message through bridge client
	_, err := h.bridge.bridgeClient.SendMessage(&req)
	if err != nil {
		return fmt.Errorf("failed to send message to XMPP room via bridge user: %w", err)
	}

	h.logger.LogDebug("Message sent via bridge user",
		"bridge_jid", bridgeJID,
		"room_jid", roomJID,
		"content_length", len(content))

	return nil
}

// formatMessageContentForBridgeUser formats message content for bridge user (with username prefix)
func (h *xmppMessageHandler) formatMessageContentForBridgeUser(msg *pluginModel.BridgeMessage) string {
	// Prefix with the original user name for bridge user messages
	if msg.SourceUserName != "" {
		return fmt.Sprintf("<%s> %s", msg.SourceUserName, msg.Content)
	}
	return msg.Content
}

// xmppUserResolver handles user resolution for the XMPP bridge
type xmppUserResolver struct {
	bridge *xmppBridge
	logger logger.Logger
}

// newUserResolver creates a new XMPP user resolver
func newUserResolver(bridge *xmppBridge) *xmppUserResolver {
	return &xmppUserResolver{
		bridge: bridge,
		logger: bridge.logger,
	}
}

// ResolveUser converts an external user ID to an ExternalUser
func (r *xmppUserResolver) ResolveUser(externalUserID string) (*pluginModel.ExternalUser, error) {
	r.logger.LogDebug("Resolving XMPP user", "user_id", externalUserID)

	// For XMPP, the external user ID is typically the full JID
	return &pluginModel.ExternalUser{
		BridgeType:       "xmpp",
		ExternalUserID:   externalUserID,
		DisplayName:      r.GetDisplayName(externalUserID),
		MattermostUserID: "", // Will be resolved by user mapping system
	}, nil
}

// FormatUserMention formats a user mention for Markdown content
func (r *xmppUserResolver) FormatUserMention(user *pluginModel.ExternalUser) string {
	// For XMPP, we can format mentions as simple text with the display name
	return fmt.Sprintf("@%s", user.DisplayName)
}

// GetDisplayName extracts display name from external user ID
func (r *xmppUserResolver) GetDisplayName(externalUserID string) string {
	// For XMPP JIDs, extract the local part or resource as display name
	// Format: user@domain/resource -> use resource or user
	if externalUserID == "" {
		return "Unknown User"
	}

	// Try to parse as JID and extract meaningful display name
	parts := strings.Split(externalUserID, "/")
	if len(parts) > 1 {
		// Has resource part, use it as display name
		return parts[1]
	}

	// No resource, try to extract local part from user@domain
	atIndex := strings.Index(externalUserID, "@")
	if atIndex > 0 {
		return externalUserID[:atIndex]
	}

	// Fallback to the full ID
	return externalUserID
}
