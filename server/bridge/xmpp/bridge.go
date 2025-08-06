package xmpp

import (
	"context"
	"crypto/tls"
	"sync"
	"sync/atomic"
	"time"

	"fmt"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/bridge"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/config"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	pluginModel "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/store/kvstore"
	xmppClient "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/xmpp"
	"github.com/mattermost/mattermost/server/public/plugin"
	"mellium.im/xmlstream"
	"mellium.im/xmpp/stanza"
)

const (
	// defaultMessageBufferSize is the buffer size for incoming message channels
	defaultMessageBufferSize = 1000
)

// xmppBridge handles syncing messages between Mattermost and XMPP
type xmppBridge struct {
	logger       logger.Logger
	api          plugin.API
	kvstore      kvstore.KVStore
	bridgeClient *xmppClient.Client // Main bridge XMPP client connection
	userManager  pluginModel.BridgeUserManager
	bridgeID     string             // Bridge identifier used for registration
	remoteID     string             // Remote ID for shared channels

	// Message handling
	messageHandler   *xmppMessageHandler
	userResolver     *xmppUserResolver
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

// NewBridge creates a new XMPP bridge
func NewBridge(log logger.Logger, api plugin.API, kvstore kvstore.KVStore, cfg *config.Configuration, bridgeID, remoteID string) pluginModel.Bridge {
	ctx, cancel := context.WithCancel(context.Background())
	b := &xmppBridge{
		logger:           log,
		api:              api,
		kvstore:          kvstore,
		ctx:              ctx,
		cancel:           cancel,
		channelMappings:  make(map[string]string),
		config:           cfg,
		userManager:      bridge.NewUserManager(bridgeID, log),
		incomingMessages: make(chan *pluginModel.DirectionalMessage, defaultMessageBufferSize),
		bridgeID:         bridgeID,
		remoteID:         remoteID,
	}

	// Initialize handlers after bridge is created
	b.messageHandler = newMessageHandler(b)
	b.userResolver = newUserResolver(b)

	// Initialize XMPP client with configuration
	if cfg.EnableSync && cfg.XMPPServerURL != "" && cfg.XMPPUsername != "" && cfg.XMPPPassword != "" {
		b.bridgeClient = b.createXMPPClient(cfg)
	}

	return b
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
		"", // remoteID not needed for bridge client
		tlsConfig,
		b.logger,
	)
}

// getConfiguration safely retrieves the current configuration
func (b *xmppBridge) getConfiguration() *config.Configuration {
	b.configMu.RLock()
	defer b.configMu.RUnlock()
	return b.config
}

// UpdateConfiguration updates the bridge configuration
// It handles validation and reconnection logic when the configuration changes
func (b *xmppBridge) UpdateConfiguration(cfg *config.Configuration) error {
	// Validate configuration using built-in validation
	if err := cfg.IsValid(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Get current config to check if restart is needed
	oldConfig := b.getConfiguration()

	// Update configuration under lock, then release immediately
	b.configMu.Lock()
	b.config = cfg

	// Initialize or update XMPP client with new configuration
	if !cfg.Equals(oldConfig) {
		if b.bridgeClient != nil && b.bridgeClient.Disconnect() != nil {
			b.logger.LogError("Failed to disconnect old XMPP bridge client")
		}
		b.bridgeClient = b.createXMPPClient(cfg)
	}
	b.configMu.Unlock()

	// Stop the bridge
	if err := b.Stop(); err != nil {
		b.logger.LogWarn("Error stopping bridge during restart", "error", err)
	}

	// Start the bridge with new configuration
	// Start() method already uses getConfiguration() safely
	if err := b.Start(); err != nil {
		b.logger.LogError("Failed to restart bridge with new configuration", "error", err)
		return fmt.Errorf("failed to restart bridge: %w", err)
	}

	b.logger.LogDebug("XMPP bridge configuration updated successfully")

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

	if b.bridgeClient != nil {
		if err := b.bridgeClient.Disconnect(); err != nil {
			b.logger.LogWarn("Error disconnecting from XMPP server", "error", err)
		}
	}

	b.connected.Store(false)
	b.logger.LogInfo("Mattermost to XMPP bridge stopped")
	return nil
}

// connectToXMPP establishes connection to the XMPP server
func (b *xmppBridge) connectToXMPP() error {
	if b.bridgeClient == nil {
		return fmt.Errorf("XMPP client is not initialized")
	}

	b.logger.LogDebug("Connecting to XMPP server")

	err := b.bridgeClient.Connect()
	if err != nil {
		b.connected.Store(false)
		return fmt.Errorf("failed to connect to XMPP server: %w", err)
	}

	b.connected.Store(true)
	b.logger.LogInfo("Successfully connected to XMPP server")

	// Set online presence after successful connection
	if err := b.bridgeClient.SetOnlinePresence(); err != nil {
		b.logger.LogWarn("Failed to set online presence", "error", err)
		// Don't fail the connection for presence issues
	} else {
		b.logger.LogDebug("Set bridge client online presence")
	}

	b.bridgeClient.SetMessageHandler(b.handleIncomingXMPPMessage)

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

	err := b.bridgeClient.JoinRoom(roomJID)
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

	if b.bridgeClient != nil {
		_ = b.bridgeClient.Disconnect()
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

	if b.bridgeClient == nil {
		return fmt.Errorf("XMPP client not initialized")
	}

	b.logger.LogDebug("Testing XMPP bridge connectivity with ping")

	// Use the XMPP client's ping method
	if err := b.bridgeClient.Ping(); err != nil {
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
		if err := b.bridgeClient.JoinRoom(roomJID); err != nil {
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
	if b.connected.Load() && b.bridgeClient != nil {
		if err := b.bridgeClient.LeaveRoom(roomJID); err != nil {
			b.logger.LogWarn("Failed to leave unmapped room", "channel_id", channelID, "room_jid", roomJID, "error", err)
			// Don't fail the entire operation if leaving the room fails
		} else {
			b.logger.LogInfo("Left XMPP room after unmapping", "channel_id", channelID, "room_jid", roomJID)
		}
	}

	b.logger.LogInfo("Deleted channel room mapping", "channel_id", channelID, "room_jid", roomJID)
	return nil
}

// ChannelMappingExists checks if an XMPP room exists on the remote service
func (b *xmppBridge) ChannelMappingExists(roomID string) (bool, error) {
	if !b.connected.Load() {
		return false, fmt.Errorf("not connected to XMPP server")
	}

	if b.bridgeClient == nil {
		return false, fmt.Errorf("XMPP client not initialized")
	}

	b.logger.LogDebug("Checking if XMPP room exists", "room_jid", roomID)

	// Use the XMPP client to check room existence
	exists, err := b.bridgeClient.CheckRoomExists(roomID)
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

// GetChannelMappingForBridge retrieves the Mattermost channel ID for a given room ID from a specific bridge
func (b *xmppBridge) GetChannelMappingForBridge(bridgeName, roomID string) (string, error) {
	// Look up the channel ID using the bridge name and room ID as the key
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
func (b *xmppBridge) GetUserManager() pluginModel.BridgeUserManager {
	return b.userManager
}


// GetMessageChannel returns the channel for incoming messages from XMPP
func (b *xmppBridge) GetMessageChannel() <-chan *pluginModel.DirectionalMessage {
	return b.incomingMessages
}

// SendMessage sends a message to an XMPP room
func (b *xmppBridge) SendMessage(msg *pluginModel.BridgeMessage) error {
	return b.messageHandler.sendMessageToXMPP(msg)
}

// GetMessageHandler returns the message handler for this bridge
func (b *xmppBridge) GetMessageHandler() pluginModel.MessageHandler {
	return b.messageHandler
}

// GetUserResolver returns the user resolver for this bridge
func (b *xmppBridge) GetUserResolver() pluginModel.UserResolver {
	return b.userResolver
}

// GetRemoteID returns the remote ID used for shared channels registration
func (b *xmppBridge) GetRemoteID() string {
	return b.remoteID
}

// ID returns the bridge identifier used when registering the bridge
func (b *xmppBridge) ID() string {
	return b.bridgeID
}

// handleIncomingXMPPMessage handles incoming XMPP messages and converts them to bridge messages
func (b *xmppBridge) handleIncomingXMPPMessage(msg stanza.Message, t xmlstream.TokenReadEncoder) error {
	b.logger.LogDebug("XMPP bridge handling incoming message",
		"from", msg.From.String(),
		"to", msg.To.String(),
		"type", fmt.Sprintf("%v", msg.Type))

	// Only process groupchat messages for now (MUC messages from channels)
	if msg.Type != stanza.GroupChatMessage {
		b.logger.LogDebug("Ignoring non-groupchat message", "type", fmt.Sprintf("%v", msg.Type))
		return nil
	}

	// Extract message body using client method
	messageBody, err := b.bridgeClient.ExtractMessageBody(t)
	if err != nil {
		b.logger.LogWarn("Failed to extract message body", "error", err)
		return nil
	}

	if messageBody == "" {
		b.logger.LogDebug("Ignoring message with empty body")
		return nil
	}

	// Use client methods for protocol normalization
	channelID, err := b.bridgeClient.ExtractChannelID(msg.From)
	if err != nil {
		return fmt.Errorf("failed to extract channel ID: %w", err)
	}

	userID, displayName := b.bridgeClient.ExtractUserInfo(msg.From)

	// Create bridge message
	bridgeMessage := &pluginModel.BridgeMessage{
		SourceBridge:    b.bridgeID,
		SourceChannelID: channelID,
		SourceUserID:    userID,
		SourceUserName:  displayName,
		SourceRemoteID:  b.remoteID,
		Content:         messageBody,
		MessageType:     "text",
		Timestamp:       time.Now(), // TODO: Parse timestamp from message if available
		MessageID:       msg.ID,
		TargetBridges:   []string{"mattermost"}, // Route to Mattermost
	}

	// Create directional message for incoming (XMPP -> Mattermost)
	directionalMessage := &pluginModel.DirectionalMessage{
		BridgeMessage: bridgeMessage,
		Direction:     pluginModel.DirectionIncoming,
	}

	// Send to bridge's message channel
	select {
	case b.incomingMessages <- directionalMessage:
		b.logger.LogDebug("XMPP message queued for processing",
			"channel_id", channelID,
			"user_id", userID,
			"message_id", msg.ID)
	default:
		b.logger.LogWarn("Bridge message channel full, dropping message",
			"channel_id", channelID,
			"user_id", userID)
	}

	return nil
}
