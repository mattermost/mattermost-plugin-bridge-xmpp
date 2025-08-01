package xmpp

import (
	"context"
	"crypto/tls"
	"sync"
	"sync/atomic"
	"time"

	"fmt"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/config"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	pluginModel "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/store/kvstore"
	xmppClient "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/xmpp"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// xmppBridge handles syncing messages between Mattermost and XMPP
type xmppBridge struct {
	logger     logger.Logger
	api        plugin.API
	kvstore    kvstore.KVStore
	xmppClient *xmppClient.Client

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
	bridge := &xmppBridge{
		logger:          log,
		api:             api,
		kvstore:         kvstore,
		ctx:             ctx,
		cancel:          cancel,
		channelMappings: make(map[string]string),
		config:          cfg,
	}

	// Initialize XMPP client with configuration
	if cfg.EnableSync && cfg.XMPPServerURL != "" && cfg.XMPPUsername != "" && cfg.XMPPPassword != "" {
		bridge.xmppClient = bridge.createXMPPClient(cfg)
	}

	return bridge
}

// createXMPPClient creates an XMPP client with the given configuration
func (b *xmppBridge) createXMPPClient(cfg *config.Configuration) *xmppClient.Client {
	// Create TLS config based on certificate verification setting
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.XMPPInsecureSkipVerify,
	}

	return xmppClient.NewClientWithTLS(
		cfg.XMPPServerURL,
		cfg.XMPPUsername,
		cfg.XMPPPassword,
		cfg.GetXMPPResource(),
		"", // remoteID not needed for bridge user
		tlsConfig,
	)
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

	// Initialize or update XMPP client with new configuration
	if cfg.EnableSync {
		if cfg.XMPPServerURL == "" || cfg.XMPPUsername == "" || cfg.XMPPPassword == "" {
			b.configMu.Unlock()
			return fmt.Errorf("XMPP server URL, username, and password are required when sync is enabled")
		}

		b.xmppClient = b.createXMPPClient(cfg)
	} else {
		b.xmppClient = nil
	}

	b.configMu.Unlock()

	// Check if we need to restart the bridge due to configuration changes
	wasConnected := b.connected.Load()
	needsRestart := oldConfig != nil && !oldConfig.Equals(cfg) && wasConnected

	// Log the configuration change
	if needsRestart {
		b.logger.LogInfo("Configuration changed, restarting bridge", "old_config", oldConfig, "new_config", cfg)
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

	// Print the configuration for debugging
	b.logger.LogDebug("Bridge configuration", "config", config)

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

	if b.xmppClient != nil {
		if err := b.xmppClient.Disconnect(); err != nil {
			b.logger.LogWarn("Error disconnecting from XMPP server", "error", err)
		}
	}

	b.connected.Store(false)
	b.logger.LogInfo("Mattermost to XMPP bridge stopped")
	return nil
}

// connectToXMPP establishes connection to the XMPP server
func (b *xmppBridge) connectToXMPP() error {
	if b.xmppClient == nil {
		return fmt.Errorf("XMPP client is not initialized")
	}

	b.logger.LogDebug("Connecting to XMPP server")

	err := b.xmppClient.Connect()
	if err != nil {
		b.connected.Store(false)
		return fmt.Errorf("failed to connect to XMPP server: %w", err)
	}

	b.connected.Store(true)
	b.logger.LogInfo("Successfully connected to XMPP server")

	// Set online presence after successful connection
	if err := b.xmppClient.SetOnlinePresence(); err != nil {
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

	err := b.xmppClient.JoinRoom(roomJID)
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
			if err := b.checkConnection(); err != nil {
				b.logger.LogWarn("XMPP connection check failed", "error", err)
				b.handleReconnection()
			}
		}
	}
}

// checkConnection verifies the XMPP connection is still active
func (b *xmppBridge) checkConnection() error {
	if !b.connected.Load() {
		return fmt.Errorf("not connected")
	}
	return b.xmppClient.TestConnection()
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

	if b.xmppClient != nil {
		b.xmppClient.Disconnect()
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

// CreateChannelRoomMapping creates a mapping between a Mattermost channel and XMPP room
func (b *xmppBridge) CreateChannelRoomMapping(channelID, roomJID string) error {
	if b.kvstore == nil {
		return fmt.Errorf("KV store not initialized")
	}

	// Store forward and reverse mappings using bridge-agnostic keys
	err := b.kvstore.Set(kvstore.BuildChannelMapKey("mattermost", channelID), []byte(roomJID))
	if err != nil {
		return fmt.Errorf("failed to store channel room mapping: %w", err)
	}

	err = b.kvstore.Set(kvstore.BuildChannelMapKey("xmpp", roomJID), []byte(channelID))
	if err != nil {
		return fmt.Errorf("failed to store reverse room mapping: %w", err)
	}

	// Update local cache
	b.mappingsMu.Lock()
	b.channelMappings[channelID] = roomJID
	b.mappingsMu.Unlock()

	// Join the room if connected
	if b.connected.Load() {
		if err := b.xmppClient.JoinRoom(roomJID); err != nil {
			b.logger.LogWarn("Failed to join newly mapped room", "channel_id", channelID, "room_jid", roomJID, "error", err)
		}
	}

	b.logger.LogInfo("Created channel room mapping", "channel_id", channelID, "room_jid", roomJID)
	return nil
}

// GetChannelRoomMapping gets the XMPP room JID for a Mattermost channel
func (b *xmppBridge) GetChannelRoomMapping(channelID string) (string, error) {
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
	roomJIDBytes, err := b.kvstore.Get(kvstore.BuildChannelMapKey("mattermost", channelID))
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
