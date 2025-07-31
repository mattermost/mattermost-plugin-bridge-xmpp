package model

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
