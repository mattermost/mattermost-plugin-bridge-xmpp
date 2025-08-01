package model

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
}

type Bridge interface {
	// UpdateConfiguration updates the bridge configuration
	UpdateConfiguration(config any) error

	// Start starts the bridge
	Start() error

	// Stop stops the bridge
	Stop() error

	// CreateChannelRoomMapping creates a mapping between a Mattermost channel ID and an bridge room ID.
	CreateChannelRoomMapping(channelID, roomJID string) error

	// GetChannelRoomMapping retrieves the bridge room ID for a given Mattermost channel ID.
	GetChannelRoomMapping(channelID string) (string, error)

	// IsConnected checks if the bridge is connected to the remote service.
	IsConnected() bool
}
