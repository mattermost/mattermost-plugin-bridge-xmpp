package model

import (
	"context"
	"fmt"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/config"
)

type BridgeID string

type UserState int

const (
	UserStateOnline UserState = iota
	UserStateAway
	UserStateBusy
	UserStateOffline
)

// CreateChannelMappingRequest contains information needed to create a channel mapping
type CreateChannelMappingRequest struct {
	ChannelID       string // Mattermost channel ID
	BridgeName      string // Name of the bridge (e.g., "xmpp")
	BridgeChannelID string // Remote room/channel ID (e.g., JID for XMPP)
	UserID          string // ID of user who triggered the mapping creation
	TeamID          string // Team ID where the channel belongs
}

// Validate checks if all required fields are present and valid
func (r CreateChannelMappingRequest) Validate() error {
	if r.ChannelID == "" {
		return fmt.Errorf("channelID cannot be empty")
	}
	if r.BridgeName == "" {
		return fmt.Errorf("bridgeName cannot be empty")
	}
	if r.BridgeChannelID == "" {
		return fmt.Errorf("bridgeChannelID cannot be empty")
	}
	if r.UserID == "" {
		return fmt.Errorf("userID cannot be empty")
	}
	if r.TeamID == "" {
		return fmt.Errorf("teamID cannot be empty")
	}
	return nil
}

// DeleteChannelMappingRequest contains information needed to delete a channel mapping
type DeleteChannelMappingRequest struct {
	ChannelID  string // Mattermost channel ID
	BridgeName string // Name of the bridge (e.g., "xmpp")
	UserID     string // ID of user who triggered the mapping deletion
	TeamID     string // Team ID where the channel belongs
}

// Validate checks if all required fields are present and valid
func (r DeleteChannelMappingRequest) Validate() error {
	if r.ChannelID == "" {
		return fmt.Errorf("channelID cannot be empty")
	}
	if r.BridgeName == "" {
		return fmt.Errorf("bridgeName cannot be empty")
	}
	if r.UserID == "" {
		return fmt.Errorf("userID cannot be empty")
	}
	if r.TeamID == "" {
		return fmt.Errorf("teamID cannot be empty")
	}
	return nil
}

type BridgeManager interface {
	// Start starts the bridge manager and message routing system.
	Start() error

	// RegisterBridge registers a bridge with the given name. Returns an error if the name is empty,
	// the bridge is nil, or a bridge with the same name is already registered.
	RegisterBridge(name string, bridge Bridge) error

	// StartBridge starts the bridge with the given name. Returns an error if the bridge
	// is not registered or fails to start.
	StartBridge(name string) error

	// StopBridge stops the bridge with the given name. Returns an error if the bridge
	// is not registered or fails to stop.
	StopBridge(name string) error

	// UnregisterBridge removes the bridge with the given name from the manager.
	// The bridge is stopped before removal if it's currently connected.
	// Returns an error if the bridge is not registered.
	UnregisterBridge(name string) error

	// GetBridge retrieves the bridge instance with the given name.
	// Returns an error if the bridge is not registered.
	GetBridge(name string) (Bridge, error)

	// ListBridges returns a list of all registered bridge names.
	ListBridges() []string

	// HasBridge checks if a bridge with the given name is registered.
	HasBridge(name string) bool

	// HasBridges checks if any bridges are currently registered.
	HasBridges() bool

	// Shutdown stops and unregisters all bridges. Returns an error if any bridge
	// fails to stop, but continues to attempt stopping all bridges.
	Shutdown() error

	// OnPluginConfigurationChange propagates configuration changes to all registered bridges.
	// Returns an error if any bridge fails to update its configuration, but continues to
	// attempt updating all bridges.
	OnPluginConfigurationChange(config *config.Configuration) error

	// CreateChannelMapping is called when a channel mapping is created.
	CreateChannelMapping(req CreateChannelMappingRequest) error

	// DeleteChannepMapping is called when a channel mapping is deleted.
	DeleteChannepMapping(req DeleteChannelMappingRequest) error

	// PublishMessage publishes a message to the message bus for routing to target bridges
	PublishMessage(msg *DirectionalMessage) error
}

type Bridge interface {
	// UpdateConfiguration updates the bridge configuration
	UpdateConfiguration(config *config.Configuration) error

	// Start starts the bridge
	Start() error

	// Stop stops the bridge
	Stop() error

	// CreateChannelMapping creates a mapping between a Mattermost channel ID and a bridge channel ID.
	CreateChannelMapping(channelID, roomJID string) error

	// GetChannelMapping retrieves the bridge channel ID for a given Mattermost channel ID.
	GetChannelMapping(channelID string) (string, error)

	// DeleteChannelMapping removes a mapping between a Mattermost channel ID and a bridge channel ID.
	DeleteChannelMapping(channelID string) error

	// ChannelMappingExists checks if a room/channel exists on the remote service.
	ChannelMappingExists(roomID string) (bool, error)

	// GetChannelMappingForBridge retrieves the Mattermost channel ID for a given room ID from a specific bridge.
	GetChannelMappingForBridge(bridgeName, roomID string) (string, error)

	// IsConnected checks if the bridge is connected to the remote service.
	IsConnected() bool

	// Ping actively tests the bridge connection health by sending a lightweight request.
	Ping() error

	// GetUserManager returns the user manager for this bridge.
	GetUserManager() BridgeUserManager

	// Message handling for bidirectional communication
	GetMessageChannel() <-chan *DirectionalMessage
	SendMessage(msg *BridgeMessage) error
	GetMessageHandler() MessageHandler
	GetUserResolver() UserResolver

	// GetRemoteID returns the remote ID used for shared channels registration
	GetRemoteID() string

	// ID returns the bridge identifier used when registering the bridge
	ID() string
}

// BridgeUser represents a user connected to any bridge service
type BridgeUser interface {
	// Validation
	Validate() error

	// Identity (bridge-agnostic)
	GetID() string
	GetDisplayName() string

	// State management
	GetState() UserState
	SetState(state UserState) error

	// Channel operations (abstracted from rooms/channels/groups)
	JoinChannel(channelID string) error
	LeaveChannel(channelID string) error
	SendMessageToChannel(channelID, message string) error

	// Connection lifecycle
	Connect() error
	Disconnect() error
	IsConnected() bool
	Ping() error

	// Channel existence check
	CheckChannelExists(channelID string) (bool, error)

	// Goroutine lifecycle
	Start(ctx context.Context) error
	Stop() error
}

// BridgeUserManager manages users for a specific bridge
type BridgeUserManager interface {
	// User lifecycle
	CreateUser(user BridgeUser) error
	GetUser(userID string) (BridgeUser, error)
	DeleteUser(userID string) error
	ListUsers() []BridgeUser
	HasUser(userID string) bool

	// Manager lifecycle
	Start(ctx context.Context) error
	Stop() error

	// Configuration updates
	UpdateConfiguration(config *config.Configuration) error

	// Bridge type identification
	GetBridgeType() string
}
