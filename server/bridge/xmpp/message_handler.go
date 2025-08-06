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
	if h.bridge.bridgeClient == nil {
		return fmt.Errorf("XMPP client not initialized")
	}

	if !h.bridge.connected.Load() {
		return fmt.Errorf("not connected to XMPP server")
	}

	// Get the XMPP room JID from the channel mapping
	roomJID, err := h.bridge.GetChannelMapping(msg.SourceChannelID)
	if err != nil {
		return fmt.Errorf("failed to get room mapping: %w", err)
	}
	if roomJID == "" {
		return fmt.Errorf("channel is not mapped to any XMPP room")
	}

	// Format the message content with user information
	content := h.formatMessageContent(msg)

	// Create XMPP message request
	req := xmppClient.MessageRequest{
		RoomJID: roomJID,
		Message: content,
	}

	// Send the message
	_, err = h.bridge.bridgeClient.SendMessage(&req)
	if err != nil {
		return fmt.Errorf("failed to send message to XMPP room: %w", err)
	}

	h.logger.LogDebug("Message sent to XMPP room",
		"channel_id", msg.SourceChannelID,
		"room_jid", roomJID,
		"content_length", len(content))

	return nil
}

// formatMessageContent formats the message content for XMPP
func (h *xmppMessageHandler) formatMessageContent(msg *pluginModel.BridgeMessage) string {
	// For messages from other bridges, prefix with the user name
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
