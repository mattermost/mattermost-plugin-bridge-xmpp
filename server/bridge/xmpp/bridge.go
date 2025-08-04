package xmpp

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"fmt"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/bridge"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/config"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
	pluginModel "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/store/kvstore"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// xmppBridge handles syncing messages between Mattermost and XMPP
type xmppBridge struct {
	logger      logger.Logger
	api         plugin.API
	kvstore     kvstore.KVStore
	bridgeUser  model.BridgeUser // Handles the bridge user and main bridge XMPP connection
	userManager pluginModel.BridgeUserManager

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

// NewBridge creates a new XMPP bridge
func NewBridge(log logger.Logger, api plugin.API, kvstore kvstore.KVStore, cfg *config.Configuration) pluginModel.Bridge {
	ctx, cancel := context.WithCancel(context.Background())
	b := &xmppBridge{
		logger:          log,
		api:             api,
		kvstore:         kvstore,
		ctx:             ctx,
		cancel:          cancel,
		channelMappings: make(map[string]string),
		config:          cfg,
		userManager:     bridge.NewUserManager("xmpp", log),
	}

	// Initialize XMPP client with configuration
	if cfg.EnableSync && cfg.XMPPServerURL != "" && cfg.XMPPUsername != "" && cfg.XMPPPassword != "" {
		b.bridgeUser = b.createXMPPClient(cfg)
	}

	return b
}

// createXMPPClient creates an XMPP client with the given configuration
func (b *xmppBridge) createXMPPClient(cfg *config.Configuration) model.BridgeUser {
	return NewXMPPUser("_bridge_", "Bridge User", cfg.XMPPUsername, cfg, b.logger)
}

// UpdateConfiguration updates the bridge configuration
func (b *xmppBridge) UpdateConfiguration(newConfig any) error {
	cfg, ok := newConfig.(*config.Configuration)
	if !ok {
		return fmt.Errorf("invalid configuration type")
	}

	b.configMu.Lock()
	oldConfig := b.config
	b.config = cfg
	defer b.configMu.Unlock()

	b.logger.LogInfo("XMPP bridge configuration updated")

	// Initialize or update XMPP client with new configuration
	if cfg.EnableSync {
		if cfg.XMPPServerURL == "" || cfg.XMPPUsername == "" || cfg.XMPPPassword == "" {
			return fmt.Errorf("XMPP server URL, username, and password are required when sync is enabled")
		}

		b.bridgeUser = b.createXMPPClient(cfg)
	} else {
		b.bridgeUser = nil
	}

	// Check if we need to restart the bridge due to configuration changes
	wasConnected := b.connected.Load()
	needsRestart := oldConfig != nil && !oldConfig.Equals(cfg) && wasConnected

	// Log the configuration change
	if needsRestart {
		b.logger.LogInfo("Configuration changed, restarting bridge")
	} else {
		b.logger.LogInfo("Configuration updated", "config", cfg)
	}

	if needsRestart {
		b.logger.LogInfo("Configuration changed, restarting bridge")

		// Stop the bridge
		if err := b.Stop(); err != nil {
			b.logger.LogWarn("Error stopping bridge during restart", "error", err)
		}

		// Start the bridge with new configuration
		if err := b.Start(); err != nil {
			b.logger.LogError("Failed to restart bridge with new configuration", "error", err)
			return fmt.Errorf("failed to restart bridge: %w", err)
		}
	}

	return nil
}

// Start initializes the bridge and connects to XMPP
func (b *xmppBridge) Start() error {
	b.logger.LogDebug("Starting Mattermost to XMPP bridge")

	b.configMu.RLock()
	config := b.config
	b.configMu.RUnlock()

	if config == nil {
		return fmt.Errorf("bridge configuration not set")
	}

	if !config.EnableSync {
		b.logger.LogInfo("XMPP sync is disabled, bridge will not start")
		return nil
	}

	b.logger.LogInfo("Starting Mattermost to XMPP bridge", "xmpp_server", config.XMPPServerURL, "username", config.XMPPUsername)

	// Connect to XMPP server
	if err := b.connectToXMPP(); err != nil {
		return fmt.Errorf("failed to connect to XMPP server: %w", err)
	}

	// Load and join mapped channels
	if err := b.loadAndJoinMappedChannels(); err != nil {
		b.logger.LogWarn("Failed to join some mapped channels", "error", err)
	}

	// Start connection monitor
	go b.connectionMonitor()

	b.logger.LogInfo("Mattermost to XMPP bridge started successfully")
	return nil
}

// Stop shuts down the bridge
func (b *xmppBridge) Stop() error {
	b.logger.LogInfo("Stopping Mattermost to XMPP bridge")

	if b.cancel != nil {
		b.cancel()
	}

	if b.bridgeUser != nil {
		if err := b.bridgeUser.Disconnect(); err != nil {
			b.logger.LogWarn("Error disconnecting from XMPP server", "error", err)
		}
	}

	b.connected.Store(false)
	b.logger.LogInfo("Mattermost to XMPP bridge stopped")
	return nil
}

// connectToXMPP establishes connection to the XMPP server
func (b *xmppBridge) connectToXMPP() error {
	if b.bridgeUser == nil {
		return fmt.Errorf("XMPP client is not initialized")
	}

	b.logger.LogDebug("Connecting to XMPP server")

	err := b.bridgeUser.Connect()
	if err != nil {
		b.connected.Store(false)
		return fmt.Errorf("failed to connect to XMPP server: %w", err)
	}

	b.connected.Store(true)
	b.logger.LogInfo("Successfully connected to XMPP server")

	// Set online presence after successful connection
	if err := b.bridgeUser.SetState(pluginModel.UserStateOnline); err != nil {
		b.logger.LogWarn("Failed to set online presence", "error", err)
		// Don't fail the connection for presence issues
	} else {
		b.logger.LogDebug("Set bridge user online presence")
	}

	return nil
}

// loadAndJoinMappedChannels loads channel mappings and joins corresponding XMPP rooms
func (b *xmppBridge) loadAndJoinMappedChannels() error {
	b.logger.LogDebug("Loading and joining mapped channels")

	// Get all channel mappings from KV store
	mappings, err := b.getAllChannelMappings()
	if err != nil {
		return fmt.Errorf("failed to load channel mappings: %w", err)
	}

	if len(mappings) == 0 {
		b.logger.LogInfo("No channel mappings found, no rooms to join")
		return nil
	}

	b.logger.LogInfo("Found channel mappings, joining XMPP rooms", "count", len(mappings))

	// Join each mapped room
	for channelID, roomJID := range mappings {
		if err := b.joinXMPPRoom(channelID, roomJID); err != nil {
			b.logger.LogWarn("Failed to join room", "channel_id", channelID, "room_jid", roomJID, "error", err)
		}
	}

	return nil
}

// joinXMPPRoom joins an XMPP room and updates the local cache
func (b *xmppBridge) joinXMPPRoom(channelID, roomJID string) error {
	if !b.connected.Load() {
		return fmt.Errorf("not connected to XMPP server")
	}

	err := b.bridgeUser.JoinChannel(roomJID)
	if err != nil {
		return fmt.Errorf("failed to join XMPP room: %w", err)
	}

	b.logger.LogInfo("Joined XMPP room", "channel_id", channelID, "room_jid", roomJID)

	// Update local cache
	b.mappingsMu.Lock()
	b.channelMappings[channelID] = roomJID
	b.mappingsMu.Unlock()

	return nil
}

// getAllChannelMappings retrieves all channel mappings from KV store
func (b *xmppBridge) getAllChannelMappings() (map[string]string, error) {
	if b.kvstore == nil {
		return nil, fmt.Errorf("KV store not initialized")
	}

	mappings := make(map[string]string)

	// Get all keys with the XMPP room mapping prefix to find all mapped rooms
	xmppPrefix := kvstore.KeyPrefixChannelMap + "xmpp_"
	keys, err := b.kvstore.ListKeysWithPrefix(0, 1000, xmppPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to list XMPP room mapping keys: %w", err)
	}

	// Load each mapping
	for _, key := range keys {
		channelIDBytes, err := b.kvstore.Get(key)
		if err != nil {
			b.logger.LogWarn("Failed to load mapping for key", "key", key, "error", err)
			continue
		}

		// Extract room JID from the key
		roomJID := kvstore.ExtractIdentifierFromChannelMapKey(key, "xmpp")
		if roomJID == "" {
			b.logger.LogWarn("Failed to extract room JID from key", "key", key)
			continue
		}

		channelID := string(channelIDBytes)
		mappings[channelID] = roomJID
	}

	return mappings, nil
}

// connectionMonitor monitors the XMPP connection
func (b *xmppBridge) connectionMonitor() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			if err := b.Ping(); err != nil {
				b.logger.LogWarn("XMPP connection check failed", "error", err)
				b.handleReconnection()
			}
		}
	}
}

// handleReconnection attempts to reconnect to XMPP and rejoin rooms
func (b *xmppBridge) handleReconnection() {
	b.configMu.RLock()
	config := b.config
	b.configMu.RUnlock()

	if config == nil || !config.EnableSync {
		return
	}

	b.logger.LogInfo("Attempting to reconnect to XMPP server")
	b.connected.Store(false)

	if b.bridgeUser != nil {
		_ = b.bridgeUser.Disconnect()
	}

	// Retry connection with exponential backoff
	maxRetries := 3
	for i := range maxRetries {
		backoff := time.Duration(1<<uint(i)) * time.Second

		select {
		case <-b.ctx.Done():
			return
		case <-time.After(backoff):
		}

		if err := b.connectToXMPP(); err != nil {
			b.logger.LogWarn("Reconnection attempt failed", "attempt", i+1, "error", err)
			continue
		}

		if err := b.loadAndJoinMappedChannels(); err != nil {
			b.logger.LogWarn("Failed to rejoin rooms after reconnection", "error", err)
		}

		b.logger.LogInfo("Successfully reconnected to XMPP server")
		return
	}

	b.logger.LogError("Failed to reconnect to XMPP server after all attempts")
}

// Public API methods

// IsConnected returns whether the bridge is connected to XMPP
func (b *xmppBridge) IsConnected() bool {
	return b.connected.Load()
}

// Ping actively tests the XMPP connection health
func (b *xmppBridge) Ping() error {
	if !b.connected.Load() {
		return fmt.Errorf("XMPP bridge is not connected")
	}

	if b.bridgeUser == nil {
		return fmt.Errorf("XMPP client not initialized")
	}

	b.logger.LogDebug("Testing XMPP bridge connectivity with ping")

	// Use the XMPP user's ping method
	if err := b.bridgeUser.Ping(); err != nil {
		b.logger.LogWarn("XMPP bridge ping failed", "error", err)
		return fmt.Errorf("XMPP bridge ping failed: %w", err)
	}

	b.logger.LogDebug("XMPP bridge ping successful")
	return nil
}

// CreateChannelMapping creates a mapping between a Mattermost channel and XMPP room
func (b *xmppBridge) CreateChannelMapping(channelID, roomJID string) error {
	if b.kvstore == nil {
		return fmt.Errorf("KV store not initialized")
	}

	err := b.kvstore.Set(kvstore.BuildChannelMapKey("xmpp", roomJID), []byte(channelID))
	if err != nil {
		return fmt.Errorf("failed to store reverse room mapping: %w", err)
	}

	// Update local cache
	b.mappingsMu.Lock()
	b.channelMappings[channelID] = roomJID
	b.mappingsMu.Unlock()

	// Join the room if connected
	if b.connected.Load() {
		if err := b.bridgeUser.JoinChannel(roomJID); err != nil {
			b.logger.LogWarn("Failed to join newly mapped room", "channel_id", channelID, "room_jid", roomJID, "error", err)
		}
	}

	b.logger.LogInfo("Created channel room mapping", "channel_id", channelID, "room_jid", roomJID)
	return nil
}

// GetChannelMapping gets the XMPP room JID for a Mattermost channel
func (b *xmppBridge) GetChannelMapping(channelID string) (string, error) {
	// Check cache first
	b.mappingsMu.RLock()
	roomJID, exists := b.channelMappings[channelID]
	b.mappingsMu.RUnlock()

	if exists {
		return roomJID, nil
	}

	if b.kvstore == nil {
		return "", fmt.Errorf("KV store not initialized")
	}

	// Check if we have a mapping in the KV store for this channel ID
	roomJIDBytes, err := b.kvstore.Get(kvstore.BuildChannelMapKey("xmpp", channelID))
	if err != nil {
		return "", nil // Unmapped channels are expected
	}

	roomJID = string(roomJIDBytes)

	// Update cache
	b.mappingsMu.Lock()
	b.channelMappings[channelID] = roomJID
	b.mappingsMu.Unlock()

	return roomJID, nil
}

// DeleteChannelMapping removes a mapping between a Mattermost channel and XMPP room
func (b *xmppBridge) DeleteChannelMapping(channelID string) error {
	if b.kvstore == nil {
		return fmt.Errorf("KV store not initialized")
	}

	// Get the room JID from the mapping before deleting
	roomJID, err := b.GetChannelMapping(channelID)
	if err != nil {
		return fmt.Errorf("failed to get channel mapping: %w", err)
	}
	if roomJID == "" {
		return fmt.Errorf("channel is not mapped to any room")
	}

	err = b.kvstore.Delete(kvstore.BuildChannelMapKey("xmpp", roomJID))
	if err != nil {
		return fmt.Errorf("failed to delete reverse room mapping: %w", err)
	}

	// Remove from local cache
	b.mappingsMu.Lock()
	delete(b.channelMappings, channelID)
	b.mappingsMu.Unlock()

	// Leave the room if connected
	if b.connected.Load() && b.bridgeUser != nil {
		if err := b.bridgeUser.LeaveChannel(roomJID); err != nil {
			b.logger.LogWarn("Failed to leave unmapped room", "channel_id", channelID, "room_jid", roomJID, "error", err)
			// Don't fail the entire operation if leaving the room fails
		} else {
			b.logger.LogInfo("Left XMPP room after unmapping", "channel_id", channelID, "room_jid", roomJID)
		}
	}

	b.logger.LogInfo("Deleted channel room mapping", "channel_id", channelID, "room_jid", roomJID)
	return nil
}

// RoomExists checks if an XMPP room exists on the remote service
func (b *xmppBridge) RoomExists(roomID string) (bool, error) {
	if !b.connected.Load() {
		return false, fmt.Errorf("not connected to XMPP server")
	}

	if b.bridgeUser == nil {
		return false, fmt.Errorf("XMPP client not initialized")
	}

	b.logger.LogDebug("Checking if XMPP room exists", "room_jid", roomID)

	// Use the XMPP user to check room existence
	exists, err := b.bridgeUser.CheckChannelExists(roomID)
	if err != nil {
		b.logger.LogError("Failed to check room existence", "room_jid", roomID, "error", err)
		return false, fmt.Errorf("failed to check room existence: %w", err)
	}

	b.logger.LogDebug("Room existence check completed", "room_jid", roomID, "exists", exists)
	return exists, nil
}

// GetRoomMapping retrieves the Mattermost channel ID for a given XMPP room JID (reverse lookup)
func (b *xmppBridge) GetRoomMapping(roomID string) (string, error) {
	if b.kvstore == nil {
		return "", fmt.Errorf("KV store not initialized")
	}

	b.logger.LogDebug("Getting channel mapping for XMPP room", "room_jid", roomID)

	// Look up the channel ID using the room JID as the key
	channelIDBytes, err := b.kvstore.Get(kvstore.BuildChannelMapKey("xmpp", roomID))
	if err != nil {
		// No mapping found is not an error, just return empty string
		b.logger.LogDebug("No channel mapping found for room", "room_jid", roomID)
		return "", nil
	}

	channelID := string(channelIDBytes)
	b.logger.LogDebug("Found channel mapping for room", "room_jid", roomID, "channel_id", channelID)

	return channelID, nil
}

// GetUserManager returns the user manager for this bridge
func (b *xmppBridge) GetUserManager() pluginModel.BridgeUserManager {
	return b.userManager
}
