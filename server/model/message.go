package model

import (
	"time"
)

// MessageDirection indicates the direction of message flow
type MessageDirection string

const (
	DirectionIncoming MessageDirection = "incoming" // From external system to us
	DirectionOutgoing MessageDirection = "outgoing" // From us to external system
)

// BridgeMessage represents a message that can be passed between any bridge types
type BridgeMessage struct {
	// Source information
	SourceBridge    string // "xmpp", "mattermost", "slack", etc.
	SourceChannelID string // Channel ID in source system
	SourceUserID    string // User ID in source system (JID, user ID, etc.)
	SourceUserName  string // Display name in source system
	SourceRemoteID  string // Remote ID of the bridge instance that created this message

	// Message content (standardized on Markdown)
	Content     string // Markdown formatted message content
	MessageType string // "text", "image", "file", etc.

	// Metadata
	Timestamp time.Time // When message was received
	MessageID string    // Unique message ID from source
	ThreadID  string    // Thread/reply ID (if applicable)

	// Routing hints
	TargetBridges []string       // Which bridges should receive this
	Metadata      map[string]any // Bridge-specific metadata
}

// DirectionalMessage wraps a BridgeMessage with direction information
type DirectionalMessage struct {
	*BridgeMessage
	Direction MessageDirection
}

// ExternalUser represents a user from any bridge system
type ExternalUser struct {
	BridgeType       string // "xmpp", "slack", etc.
	ExternalUserID   string // JID, Slack user ID, etc.
	DisplayName      string // How to display this user
	MattermostUserID string // Mapped Mattermost user (if exists)
}

// UserResolver handles user resolution for a specific bridge
type UserResolver interface {
	// ResolveUser converts an external user ID to an ExternalUser
	ResolveUser(externalUserID string) (*ExternalUser, error)

	// FormatUserMention formats a user mention for Markdown content
	FormatUserMention(user *ExternalUser) string

	// GetDisplayName extracts display name from external user ID
	GetDisplayName(externalUserID string) string
}

// MessageBus handles routing messages between bridges
type MessageBus interface {
	// Subscribe returns a channel that receives messages for the specified bridge
	Subscribe(bridgeName string) <-chan *DirectionalMessage

	// Publish sends a message to the message bus for routing
	Publish(msg *DirectionalMessage) error

	// Start begins message routing
	Start() error

	// Stop ends message routing and cleans up resources
	Stop() error
}

// MessageHandler processes incoming messages for a bridge
type MessageHandler interface {
	// ProcessMessage handles an incoming message
	ProcessMessage(msg *DirectionalMessage) error

	// CanHandleMessage determines if this handler can process the message
	CanHandleMessage(msg *BridgeMessage) bool

	// GetSupportedMessageTypes returns the message types this handler supports
	GetSupportedMessageTypes() []string
}
