package model

type BridgeID string

type UserState int

const (
	UserStateOnline UserState = iota
	UserStateAway
	UserStateBusy
	UserStateOffline
)

type BridgeManager interface {
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
	OnPluginConfigurationChange(config any) error

	// OnChannelMappingCreated is called when a channel mapping is created.
	OnChannelMappingCreated(channelID, bridgeName, bridgeRoomID string) error

	// OnChannelMappingDeleted is called when a channel mapping is deleted.
	OnChannelMappingDeleted(channelID, bridgeName string) error
}

type Bridge interface {
	// UpdateConfiguration updates the bridge configuration
	UpdateConfiguration(config any) error

	// Start starts the bridge
	Start() error

	// Stop stops the bridge
	Stop() error

	// CreateChannelMapping creates a mapping between a Mattermost channel ID and an bridge room ID.
	CreateChannelMapping(channelID, roomJID string) error

	// GetChannelMapping retrieves the bridge room ID for a given Mattermost channel ID.
	GetChannelMapping(channelID string) (string, error)

	// DeleteChannelMapping removes a mapping between a Mattermost channel ID and a bridge room ID.
	DeleteChannelMapping(channelID string) error

	// IsConnected checks if the bridge is connected to the remote service.
	IsConnected() bool
}

type BridgeUserManager interface {
	// CreateUser creates a new user in the bridge system.
	CreateUser(userID string, userData any) error

	// GetUser retrieves user data for a given user ID.
	GetUser(userID string) (any, error)

	// UpdateUser updates user data for a given user ID.
	UpdateUser(userID string, userData any) error

	// DeleteUser removes a user from the bridge system.
	DeleteUser(userID string) error

	// ListUsers returns a list of all users in the bridge system.
	ListUsers() ([]string, error)

	// HasUser checks if a user exists in the bridge system.
	HasUser(userID string) bool

	// OnUserStateChange is called when a user's state changes (e.g., online, away, offline).
	OnUserStateChange(userID string, state UserState) error
}
