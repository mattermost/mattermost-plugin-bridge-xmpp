package mattermost

import (
	"context"
	"crypto/tls"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/config"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/store/kvstore"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/xmpp"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/pkg/errors"
)

// MattermostToXMPPBridge handles syncing messages from Mattermost to XMPP
type MattermostToXMPPBridge struct {
	logger     logger.Logger
	api        plugin.API
	kvstore    kvstore.KVStore
	xmppClient *xmpp.Client

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

// NewMattermostToXMPPBridge creates a new Mattermost to XMPP bridge
func NewMattermostToXMPPBridge(log logger.Logger, api plugin.API, kvstore kvstore.KVStore, cfg *config.Configuration) *MattermostToXMPPBridge {
	ctx, cancel := context.WithCancel(context.Background())
	bridge := &MattermostToXMPPBridge{
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
func (b *MattermostToXMPPBridge) createXMPPClient(cfg *config.Configuration) *xmpp.Client {
	// Create TLS config based on certificate verification setting
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.XMPPInsecureSkipVerify,
	}
	
	return xmpp.NewClientWithTLS(
		cfg.XMPPServerURL,
		cfg.XMPPUsername,
		cfg.XMPPPassword,
		cfg.GetXMPPResource(),
		"", // remoteID not needed for bridge user
		tlsConfig,
	)
}

// UpdateConfiguration updates the bridge configuration
func (b *MattermostToXMPPBridge) UpdateConfiguration(newConfig any) error {
	cfg, ok := newConfig.(*config.Configuration)
	if !ok {
		return errors.New("invalid configuration type")
	}

	b.configMu.Lock()
	oldConfig := b.config
	b.config = cfg

	// Initialize or update XMPP client with new configuration
	if cfg.EnableSync {
		if cfg.XMPPServerURL == "" || cfg.XMPPUsername == "" || cfg.XMPPPassword == "" {
			b.configMu.Unlock()
			return errors.New("XMPP server URL, username, and password are required when sync is enabled")
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
	if b.logger != nil {
		if needsRestart {
			b.logger.LogInfo("Configuration changed, restarting bridge", "old_config", oldConfig, "new_config", cfg)
		} else {
			b.logger.LogInfo("Configuration updated", "config", cfg)
		}
	}

	if needsRestart {
		if b.logger != nil {
			b.logger.LogInfo("Configuration changed, restarting bridge")
		}

		// Stop the bridge
		if err := b.Stop(); err != nil && b.logger != nil {
			b.logger.LogWarn("Error stopping bridge during restart", "error", err)
		}

		// Start the bridge with new configuration
		if err := b.Start(); err != nil {
			if b.logger != nil {
				b.logger.LogError("Failed to restart bridge with new configuration", "error", err)
			}
			return errors.Wrap(err, "failed to restart bridge")
		}
	}

	return nil
}

// Start initializes the bridge and connects to XMPP
func (b *MattermostToXMPPBridge) Start() error {
	b.logger.LogDebug("Starting Mattermost to XMPP bridge")

	b.configMu.RLock()
	config := b.config
	b.configMu.RUnlock()

	if config == nil {
		return errors.New("bridge configuration not set")
	}

	// Print the configuration for debugging
	b.logger.LogDebug("Bridge configuration", "config", config)

	if !config.EnableSync {
		if b.logger != nil {
			b.logger.LogInfo("XMPP sync is disabled, bridge will not start")
		}
		return nil
	}

	if b.logger != nil {
		b.logger.LogInfo("Starting Mattermost to XMPP bridge", "xmpp_server", config.XMPPServerURL, "username", config.XMPPUsername)
	}

	// Connect to XMPP server
	if err := b.connectToXMPP(); err != nil {
		return errors.Wrap(err, "failed to connect to XMPP server")
	}

	// Load and join mapped channels
	if err := b.loadAndJoinMappedChannels(); err != nil {
		if b.logger != nil {
			b.logger.LogWarn("Failed to join some mapped channels", "error", err)
		}
	}

	// Start connection monitor
	go b.connectionMonitor()

	if b.logger != nil {
		b.logger.LogInfo("Mattermost to XMPP bridge started successfully")
	}
	return nil
}

// Stop shuts down the bridge
func (b *MattermostToXMPPBridge) Stop() error {
	if b.logger != nil {
		b.logger.LogInfo("Stopping Mattermost to XMPP bridge")
	}

	if b.cancel != nil {
		b.cancel()
	}

	if b.xmppClient != nil {
		if err := b.xmppClient.Disconnect(); err != nil && b.logger != nil {
			b.logger.LogWarn("Error disconnecting from XMPP server", "error", err)
		}
	}

	b.connected.Store(false)
	if b.logger != nil {
		b.logger.LogInfo("Mattermost to XMPP bridge stopped")
	}
	return nil
}

// connectToXMPP establishes connection to the XMPP server
func (b *MattermostToXMPPBridge) connectToXMPP() error {
	if b.xmppClient == nil {
		return errors.New("XMPP client is not initialized")
	}

	if b.logger != nil {
		b.logger.LogDebug("Connecting to XMPP server")
	}

	err := b.xmppClient.Connect()
	if err != nil {
		b.connected.Store(false)
		return errors.Wrap(err, "failed to connect to XMPP server")
	}

	b.connected.Store(true)
	if b.logger != nil {
		b.logger.LogInfo("Successfully connected to XMPP server")
	}

	// Set online presence after successful connection
	if err := b.xmppClient.SetOnlinePresence(); err != nil {
		if b.logger != nil {
			b.logger.LogWarn("Failed to set online presence", "error", err)
		}
		// Don't fail the connection for presence issues
	} else if b.logger != nil {
		b.logger.LogDebug("Set bridge user online presence")
	}

	return nil
}

// loadAndJoinMappedChannels loads channel mappings and joins corresponding XMPP rooms
func (b *MattermostToXMPPBridge) loadAndJoinMappedChannels() error {
	if b.logger != nil {
		b.logger.LogDebug("Loading and joining mapped channels")
	}

	// Get all channel mappings from KV store
	mappings, err := b.getAllChannelMappings()
	if err != nil {
		return errors.Wrap(err, "failed to load channel mappings")
	}

	if len(mappings) == 0 {
		if b.logger != nil {
			b.logger.LogInfo("No channel mappings found, no rooms to join")
		}
		return nil
	}

	if b.logger != nil {
		b.logger.LogInfo("Found channel mappings, joining XMPP rooms", "count", len(mappings))
	}

	// Join each mapped room
	for channelID, roomJID := range mappings {
		if err := b.joinXMPPRoom(channelID, roomJID); err != nil && b.logger != nil {
			b.logger.LogWarn("Failed to join room", "channel_id", channelID, "room_jid", roomJID, "error", err)
		}
	}

	return nil
}

// joinXMPPRoom joins an XMPP room and updates the local cache
func (b *MattermostToXMPPBridge) joinXMPPRoom(channelID, roomJID string) error {
	if !b.connected.Load() {
		return errors.New("not connected to XMPP server")
	}

	err := b.xmppClient.JoinRoom(roomJID)
	if err != nil {
		return errors.Wrap(err, "failed to join XMPP room")
	}

	// Update local cache
	b.mappingsMu.Lock()
	b.channelMappings[channelID] = roomJID
	b.mappingsMu.Unlock()

	return nil
}

// getAllChannelMappings retrieves all channel mappings from KV store
func (b *MattermostToXMPPBridge) getAllChannelMappings() (map[string]string, error) {
	mappings := make(map[string]string)
	return mappings, nil
}

// connectionMonitor monitors the XMPP connection
func (b *MattermostToXMPPBridge) connectionMonitor() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			if err := b.checkConnection(); err != nil {
				if b.logger != nil {
					b.logger.LogWarn("XMPP connection check failed", "error", err)
				}
				b.handleReconnection()
			}
		}
	}
}

// checkConnection verifies the XMPP connection is still active
func (b *MattermostToXMPPBridge) checkConnection() error {
	if !b.connected.Load() {
		return errors.New("not connected")
	}
	return b.xmppClient.TestConnection()
}

// handleReconnection attempts to reconnect to XMPP and rejoin rooms
func (b *MattermostToXMPPBridge) handleReconnection() {
	b.configMu.RLock()
	config := b.config
	b.configMu.RUnlock()

	if config == nil || !config.EnableSync {
		return
	}

	if b.logger != nil {
		b.logger.LogInfo("Attempting to reconnect to XMPP server")
	}
	b.connected.Store(false)

	if b.xmppClient != nil {
		b.xmppClient.Disconnect()
	}

	// Retry connection with exponential backoff
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		backoff := time.Duration(1<<uint(i)) * time.Second

		select {
		case <-b.ctx.Done():
			return
		case <-time.After(backoff):
		}

		if err := b.connectToXMPP(); err != nil {
			if b.logger != nil {
				b.logger.LogWarn("Reconnection attempt failed", "attempt", i+1, "error", err)
			}
			continue
		}

		if err := b.loadAndJoinMappedChannels(); err != nil && b.logger != nil {
			b.logger.LogWarn("Failed to rejoin rooms after reconnection", "error", err)
		}

		if b.logger != nil {
			b.logger.LogInfo("Successfully reconnected to XMPP server")
		}
		return
	}

	if b.logger != nil {
		b.logger.LogError("Failed to reconnect to XMPP server after all attempts")
	}
}

// Public API methods

// IsConnected returns whether the bridge is connected to XMPP
func (b *MattermostToXMPPBridge) IsConnected() bool {
	return b.connected.Load()
}

// CreateChannelRoomMapping creates a mapping between a Mattermost channel and XMPP room
func (b *MattermostToXMPPBridge) CreateChannelRoomMapping(channelID, roomJID string) error {
	if b.kvstore == nil {
		return errors.New("KV store not initialized")
	}

	// Store forward and reverse mappings
	err := b.kvstore.Set(kvstore.BuildChannelMappingKey(channelID), []byte(roomJID))
	if err != nil {
		return errors.Wrap(err, "failed to store channel room mapping")
	}

	err = b.kvstore.Set(kvstore.BuildRoomMappingKey(roomJID), []byte(channelID))
	if err != nil {
		return errors.Wrap(err, "failed to store reverse room mapping")
	}

	// Update local cache
	b.mappingsMu.Lock()
	b.channelMappings[channelID] = roomJID
	b.mappingsMu.Unlock()

	// Join the room if connected
	if b.connected.Load() {
		if err := b.xmppClient.JoinRoom(roomJID); err != nil && b.logger != nil {
			b.logger.LogWarn("Failed to join newly mapped room", "channel_id", channelID, "room_jid", roomJID, "error", err)
		}
	}

	if b.logger != nil {
		b.logger.LogInfo("Created channel room mapping", "channel_id", channelID, "room_jid", roomJID)
	}
	return nil
}

// GetChannelRoomMapping gets the XMPP room JID for a Mattermost channel
func (b *MattermostToXMPPBridge) GetChannelRoomMapping(channelID string) (string, error) {
	// Check cache first
	b.mappingsMu.RLock()
	roomJID, exists := b.channelMappings[channelID]
	b.mappingsMu.RUnlock()

	if exists {
		return roomJID, nil
	}

	if b.kvstore == nil {
		return "", errors.New("KV store not initialized")
	}

	// Load from KV store
	roomJIDBytes, err := b.kvstore.Get(kvstore.BuildChannelMappingKey(channelID))
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
