package mattermost

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/mattermost/mattermost/server/public/plugin"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/bridge"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/config"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	pluginModel "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/store/kvstore"
)

const (
	// defaultMessageBufferSize is the buffer size for incoming message channels
	defaultMessageBufferSize = 1000
)

// mattermostBridge handles syncing messages between Mattermost instances
type mattermostBridge struct {
	logger      logger.Logger
	api         plugin.API
	kvstore     kvstore.KVStore
	userManager pluginModel.BridgeUserManager
	botUserID   string // Bot user ID for posting messages
	bridgeID    string // Bridge identifier used for registration
	remoteID    string // Remote ID for shared channels

	// Message handling
	messageHandler   *mattermostMessageHandler
	userResolver     *mattermostUserResolver
	incomingMessages chan *pluginModel.DirectionalMessage

	// Connection management
	connected atomic.Bool
	ctx       context.Context
	cancel    context.CancelFunc

	// Current configuration
	config   *config.Configuration
	configMu sync.RWMutex

	// Channel mappings cache
	channelMappings map[string]string
	mappingsMu      sync.RWMutex
}

// NewBridge creates a new Mattermost bridge
func NewBridge(log logger.Logger, api plugin.API, store kvstore.KVStore, cfg *config.Configuration, botUserID, bridgeID, remoteID string) pluginModel.Bridge {
	ctx, cancel := context.WithCancel(context.Background())
	b := &mattermostBridge{
		logger:           log,
		api:              api,
		kvstore:          store,
		botUserID:        botUserID,
		bridgeID:         bridgeID,
		remoteID:         remoteID,
		ctx:              ctx,
		cancel:           cancel,
		channelMappings:  make(map[string]string),
		config:           cfg,
		userManager:      bridge.NewUserManager(bridgeID, log),
		incomingMessages: make(chan *pluginModel.DirectionalMessage, defaultMessageBufferSize),
	}

	// Initialize handlers after bridge is created
	b.messageHandler = newMessageHandler(b)
	b.userResolver = newUserResolver(b)

	return b
}

// UpdateConfiguration updates the bridge configuration
func (b *mattermostBridge) UpdateConfiguration(cfg *config.Configuration) error {
	// Validate configuration using built-in validation
	if err := cfg.IsValid(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	b.configMu.Lock()
	b.config = cfg
	b.configMu.Unlock()

	// Log the configuration change
	b.logger.LogInfo("Mattermost bridge configuration updated")

	return nil
}

// Start initializes the bridge
func (b *mattermostBridge) Start() error {
	b.logger.LogDebug("Starting Mattermost bridge")

	b.configMu.RLock()
	cfg := b.config
	b.configMu.RUnlock()

	if cfg == nil {
		return fmt.Errorf("bridge configuration not set")
	}

	// For Mattermost bridge, we're always "connected" since we're running within Mattermost
	b.connected.Store(true)

	// Load existing channel mappings
	if err := b.loadChannelMappings(); err != nil {
		b.logger.LogWarn("Failed to load some channel mappings", "error", err)
	}

	b.logger.LogInfo("Mattermost bridge started successfully")
	return nil
}

// Stop shuts down the bridge
func (b *mattermostBridge) Stop() error {
	b.logger.LogInfo("Stopping Mattermost bridge")

	if b.cancel != nil {
		b.cancel()
	}

	b.connected.Store(false)
	b.logger.LogInfo("Mattermost bridge stopped")
	return nil
}

// loadChannelMappings loads existing channel mappings from KV store
func (b *mattermostBridge) loadChannelMappings() error {
	b.logger.LogDebug("Loading channel mappings for Mattermost bridge")

	// Get all channel mappings from KV store for Mattermost bridge
	mappings, err := b.getAllChannelMappings()
	if err != nil {
		return fmt.Errorf("failed to load channel mappings: %w", err)
	}

	if len(mappings) == 0 {
		b.logger.LogInfo("No channel mappings found for Mattermost bridge")
		return nil
	}

	b.logger.LogInfo("Found channel mappings for Mattermost bridge", "count", len(mappings))

	// Update local cache
	b.mappingsMu.Lock()
	maps.Copy(b.channelMappings, mappings)
	b.mappingsMu.Unlock()

	return nil
}

// getAllChannelMappings retrieves all channel mappings from KV store for Mattermost bridge
func (b *mattermostBridge) getAllChannelMappings() (map[string]string, error) {
	if b.kvstore == nil {
		return nil, fmt.Errorf("KV store not initialized")
	}

	mappings := make(map[string]string)

	// Get all keys with the Mattermost bridge mapping prefix
	mattermostPrefix := kvstore.KeyPrefixChannelMap + "mattermost_"
	keys, err := b.kvstore.ListKeysWithPrefix(0, 1000, mattermostPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to list Mattermost bridge mapping keys: %w", err)
	}

	// Load each mapping
	for _, key := range keys {
		channelIDBytes, err := b.kvstore.Get(key)
		if err != nil {
			b.logger.LogWarn("Failed to load mapping for key", "key", key, "error", err)
			continue
		}

		// Extract room ID from the key
		roomID := kvstore.ExtractIdentifierFromChannelMapKey(key, "mattermost")
		if roomID == "" {
			b.logger.LogWarn("Failed to extract room ID from key", "key", key)
			continue
		}

		channelID := string(channelIDBytes)
		mappings[channelID] = roomID
	}

	return mappings, nil
}

// IsConnected returns whether the bridge is connected
func (b *mattermostBridge) IsConnected() bool {
	// Mattermost bridge is always "connected" since it runs within Mattermost
	return b.connected.Load()
}

// Ping actively tests the Mattermost API connectivity
func (b *mattermostBridge) Ping() error {
	if !b.connected.Load() {
		return fmt.Errorf("mattermost bridge is not connected")
	}

	if b.api == nil {
		return fmt.Errorf("mattermost API not initialized")
	}

	b.logger.LogDebug("Testing Mattermost bridge connectivity with API ping")

	// Test API connectivity with a lightweight call
	// Using GetServerVersion as it's a simple, read-only operation
	version := b.api.GetServerVersion()
	if version == "" {
		b.logger.LogWarn("Mattermost bridge ping returned empty version")
		return fmt.Errorf("mattermost API ping returned empty server version")
	}

	b.logger.LogDebug("Mattermost bridge ping successful", "server_version", version)
	return nil
}

// CreateChannelMapping creates a mapping between a Mattermost channel and another Mattermost room/channel
func (b *mattermostBridge) CreateChannelMapping(channelID, roomID string) error {
	if b.kvstore == nil {
		return fmt.Errorf("KV store not initialized")
	}

	// Store forward and reverse mappings using bridge-agnostic keys
	err := b.kvstore.Set(kvstore.BuildChannelMapKey("mattermost", channelID), []byte(roomID))
	if err != nil {
		return fmt.Errorf("failed to store channel room mapping: %w", err)
	}

	// Update local cache
	b.mappingsMu.Lock()
	b.channelMappings[channelID] = roomID
	b.mappingsMu.Unlock()

	b.logger.LogInfo("Created Mattermost channel room mapping", "channel_id", channelID, "room_id", roomID)
	return nil
}

// GetChannelMapping gets the room ID for a Mattermost channel
func (b *mattermostBridge) GetChannelMapping(channelID string) (string, error) {
	// Check cache first
	b.mappingsMu.RLock()
	roomID, exists := b.channelMappings[channelID]
	b.mappingsMu.RUnlock()

	if exists {
		return roomID, nil
	}

	if b.kvstore == nil {
		return "", fmt.Errorf("KV store not initialized")
	}

	// Check if we have a mapping in the KV store for this channel ID
	roomIDBytes, err := b.kvstore.Get(kvstore.BuildChannelMapKey("mattermost", channelID))
	if err != nil {
		return "", nil // Unmapped channels are expected
	}

	roomID = string(roomIDBytes)

	// Update cache
	b.mappingsMu.Lock()
	b.channelMappings[channelID] = roomID
	b.mappingsMu.Unlock()

	return roomID, nil
}

// DeleteChannelMapping removes a mapping between a Mattermost channel and room
func (b *mattermostBridge) DeleteChannelMapping(channelID string) error {
	if b.kvstore == nil {
		return fmt.Errorf("KV store not initialized")
	}

	// Get the room ID from the mapping before deleting
	roomID, err := b.GetChannelMapping(channelID)
	if err != nil {
		return fmt.Errorf("failed to get channel mapping: %w", err)
	}
	if roomID == "" {
		return fmt.Errorf("channel is not mapped to any room")
	}

	// Delete forward and reverse mappings from KV store
	err = b.kvstore.Delete(kvstore.BuildChannelMapKey("mattermost", channelID))
	if err != nil {
		return fmt.Errorf("failed to delete channel room mapping: %w", err)
	}

	// Remove from local cache
	b.mappingsMu.Lock()
	delete(b.channelMappings, channelID)
	b.mappingsMu.Unlock()

	b.logger.LogInfo("Deleted Mattermost channel room mapping", "channel_id", channelID, "room_id", roomID)
	return nil
}

// ChannelMappingExists checks if a Mattermost channel exists on the server
func (b *mattermostBridge) ChannelMappingExists(roomID string) (bool, error) {
	if b.api == nil {
		return false, fmt.Errorf("mattermost API not initialized")
	}

	b.logger.LogDebug("Checking if Mattermost channel exists", "channel_id", roomID)

	// Use the Mattermost API to check if the channel exists
	channel, appErr := b.api.GetChannel(roomID)
	if appErr != nil {
		if appErr.StatusCode == 404 {
			b.logger.LogDebug("Mattermost channel does not exist", "channel_id", roomID)
			return false, nil
		}
		b.logger.LogError("Failed to check channel existence", "channel_id", roomID, "error", appErr)
		return false, fmt.Errorf("failed to check channel existence: %w", appErr)
	}

	if channel == nil {
		b.logger.LogDebug("Mattermost channel does not exist (nil response)", "channel_id", roomID)
		return false, nil
	}

	b.logger.LogDebug("Mattermost channel exists", "channel_id", roomID, "channel_name", channel.Name)
	return true, nil
}

// GetRoomMapping retrieves the Mattermost channel ID for a given room ID (reverse lookup)
func (b *mattermostBridge) GetRoomMapping(roomID string) (string, error) {
	if b.kvstore == nil {
		return "", fmt.Errorf("KV store not initialized")
	}

	b.logger.LogDebug("Getting channel mapping for Mattermost room", "room_id", roomID)

	// Look up the channel ID using the room ID as the key
	channelIDBytes, err := b.kvstore.Get(kvstore.BuildChannelMapKey("mattermost", roomID))
	if err != nil {
		// No mapping found is not an error, just return empty string
		b.logger.LogDebug("No channel mapping found for room", "room_id", roomID)
		return "", nil
	}

	channelID := string(channelIDBytes)
	b.logger.LogDebug("Found channel mapping for room", "room_id", roomID, "channel_id", channelID)

	return channelID, nil
}

// GetChannelMappingForBridge retrieves the Mattermost channel ID for a given room ID from a specific bridge
func (b *mattermostBridge) GetChannelMappingForBridge(bridgeName, roomID string) (string, error) {
	channelIDBytes, err := b.kvstore.Get(kvstore.BuildChannelMapKey(bridgeName, roomID))
	if err != nil {
		// No mapping found is not an error, just return empty string
		b.logger.LogDebug("No channel mapping found for bridge room", "bridge_name", bridgeName, "room_id", roomID)
		return "", nil
	}

	channelID := string(channelIDBytes)
	b.logger.LogDebug("Found channel mapping for bridge room", "bridge_name", bridgeName, "room_id", roomID, "channel_id", channelID)

	return channelID, nil
}

// GetUserManager returns the user manager for this bridge
func (b *mattermostBridge) GetUserManager() pluginModel.BridgeUserManager {
	return b.userManager
}

// GetMessageChannel returns the channel for incoming messages from Mattermost
func (b *mattermostBridge) GetMessageChannel() <-chan *pluginModel.DirectionalMessage {
	return b.incomingMessages
}

// SendMessage sends a message to a Mattermost channel
func (b *mattermostBridge) SendMessage(msg *pluginModel.BridgeMessage) error {
	return b.messageHandler.postMessageToMattermost(msg)
}

// GetMessageHandler returns the message handler for this bridge
func (b *mattermostBridge) GetMessageHandler() pluginModel.MessageHandler {
	return b.messageHandler
}

// GetUserResolver returns the user resolver for this bridge
func (b *mattermostBridge) GetUserResolver() pluginModel.UserResolver {
	return b.userResolver
}

// GetRemoteID returns the remote ID used for shared channels registration
func (b *mattermostBridge) GetRemoteID() string {
	return b.remoteID
}

// ID returns the bridge identifier used when registering the bridge
func (b *mattermostBridge) ID() string {
	return b.bridgeID
}
