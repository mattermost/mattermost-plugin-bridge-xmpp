package bridge

import (
	"context"
	"fmt"
	"sync"

	mmModel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/config"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
)

// BridgeManager manages multiple bridge instances
//
//nolint:revive // BridgeManager is clearer than Manager in this context
type BridgeManager struct {
	bridges       map[string]model.Bridge
	mu            sync.RWMutex
	logger        logger.Logger
	api           plugin.API
	remoteID      string
	messageBus    model.MessageBus
	routingCtx    context.Context
	routingCancel context.CancelFunc
	routingWg     sync.WaitGroup
}

// NewBridgeManager creates a new bridge manager
func NewBridgeManager(log logger.Logger, api plugin.API, remoteID string) model.BridgeManager {
	ctx, cancel := context.WithCancel(context.Background())

	return &BridgeManager{
		bridges:       make(map[string]model.Bridge),
		logger:        log,
		api:           api,
		remoteID:      remoteID,
		messageBus:    NewMessageBus(log),
		routingCtx:    ctx,
		routingCancel: cancel,
	}
}

// RegisterBridge registers a bridge with the manager
func (m *BridgeManager) RegisterBridge(name string, bridge model.Bridge) error {
	if name == "" {
		return fmt.Errorf("bridge name cannot be empty")
	}
	if bridge == nil {
		return fmt.Errorf("bridge cannot be nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.bridges[name]; exists {
		return fmt.Errorf("bridge '%s' is already registered", name)
	}

	m.bridges[name] = bridge
	m.logger.LogInfo("Bridge registered", "bridge_id", name)

	// Subscribe bridge to message bus
	go m.startBridgeMessageHandler(name, bridge)

	return nil
}

// StartBridge starts a specific bridge
func (m *BridgeManager) StartBridge(name string) error {
	m.mu.RLock()
	bridge, exists := m.bridges[name]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("bridge '%s' is not registered", name)
	}

	m.logger.LogInfo("Starting bridge", "bridge_id", name)

	if err := bridge.Start(); err != nil {
		m.logger.LogError("Failed to start bridge", "bridge_id", name, "error", err)
		return fmt.Errorf("failed to start bridge '%s': %w", name, err)
	}

	m.logger.LogInfo("Bridge started successfully", "bridge_id", name)
	return nil
}

// StopBridge stops a specific bridge
func (m *BridgeManager) StopBridge(name string) error {
	m.mu.RLock()
	bridge, exists := m.bridges[name]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("bridge '%s' is not registered", name)
	}

	m.logger.LogInfo("Stopping bridge", "bridge_id", name)

	if err := bridge.Stop(); err != nil {
		m.logger.LogError("Failed to stop bridge", "bridge_id", name, "error", err)
		return fmt.Errorf("failed to stop bridge '%s': %w", name, err)
	}

	m.logger.LogInfo("Bridge stopped successfully", "bridge_id", name)
	return nil
}

// UnregisterBridge removes a bridge from the manager
func (m *BridgeManager) UnregisterBridge(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bridge, exists := m.bridges[name]
	if !exists {
		return fmt.Errorf("bridge '%s' is not registered", name)
	}

	// Stop the bridge before unregistering
	if bridge.IsConnected() {
		if err := bridge.Stop(); err != nil {
			m.logger.LogWarn("Failed to stop bridge during unregistration", "bridge_id", name, "error", err)
		}
	}

	delete(m.bridges, name)
	m.logger.LogInfo("Bridge unregistered", "bridge_id", name)

	return nil
}

// GetBridge retrieves a bridge by name
func (m *BridgeManager) GetBridge(name string) (model.Bridge, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bridge, exists := m.bridges[name]
	if !exists {
		return nil, fmt.Errorf("bridge '%s' is not registered", name)
	}

	return bridge, nil
}

// ListBridges returns a list of all registered bridge names
func (m *BridgeManager) ListBridges() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bridges := make([]string, 0, len(m.bridges))
	for name := range m.bridges {
		bridges = append(bridges, name)
	}

	return bridges
}

// Start starts the bridge manager and message routing system
func (m *BridgeManager) Start() error {
	m.logger.LogInfo("Starting bridge manager")

	// Start the message routing system
	if err := m.StartMessageRouting(); err != nil {
		m.logger.LogError("Failed to start message routing", "error", err)
		return fmt.Errorf("failed to start message routing: %w", err)
	}

	m.logger.LogInfo("Bridge manager started successfully")
	return nil
}

// HasBridge checks if a bridge with the given name is registered
func (m *BridgeManager) HasBridge(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, exists := m.bridges[name]
	return exists
}

// HasBridges checks if any bridges are registered
func (m *BridgeManager) HasBridges() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.bridges) > 0
}

// Shutdown stops and unregisters all bridges
func (m *BridgeManager) Shutdown() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.logger.LogInfo("Shutting down bridge manager", "bridge_count", len(m.bridges))

	// Stop message routing first
	if err := m.StopMessageRouting(); err != nil {
		m.logger.LogError("Failed to stop message routing during shutdown", "error", err)
	}

	var errors []error
	for name, bridge := range m.bridges {
		if bridge.IsConnected() {
			if err := bridge.Stop(); err != nil {
				errors = append(errors, fmt.Errorf("failed to stop bridge '%s': %w", name, err))
				m.logger.LogError("Failed to stop bridge during shutdown", "bridge_id", name, "error", err)
			}
		}
	}

	// Clear all bridges
	m.bridges = make(map[string]model.Bridge)

	m.logger.LogInfo("Bridge manager shutdown complete")

	if len(errors) > 0 {
		return fmt.Errorf("shutdown completed with errors: %v", errors)
	}

	return nil
}

// OnPluginConfigurationChange propagates configuration changes to all registered bridges
func (m *BridgeManager) OnPluginConfigurationChange(cfg *config.Configuration) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.bridges) == 0 {
		return nil
	}

	m.logger.LogInfo("Plugin configuration changed, propagating to bridges", "bridge_count", len(m.bridges))

	var errors []error
	for name, bridge := range m.bridges {
		if err := bridge.UpdateConfiguration(cfg); err != nil {
			errors = append(errors, fmt.Errorf("failed to update configuration for bridge '%s': %w", name, err))
			m.logger.LogError("Failed to update bridge configuration", "bridge_id", name, "error", err)
		} else {
			m.logger.LogDebug("Successfully updated bridge configuration", "bridge_id", name)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("configuration update completed with errors: %v", errors)
	}

	m.logger.LogInfo("Configuration changes propagated to all bridges")
	return nil
}

// CreateChannelMapping handles the creation of a channel mapping by calling the appropriate bridge
func (m *BridgeManager) CreateChannelMapping(req *model.CreateChannelMappingRequest) error {
	// Validate request
	if err := req.Validate(); err != nil {
		return fmt.Errorf("invalid mapping request: %w", err)
	}

	m.logger.LogDebug("Creating channel mapping", "channel_id", req.ChannelID, "bridge_name", req.BridgeName, "bridge_channel_id", req.BridgeChannelID, "user_id", req.UserID, "team_id", req.TeamID)

	// Get the specific bridge
	bridge, err := m.GetBridge(req.BridgeName)
	if err != nil {
		m.logger.LogError("Failed to get bridge", "bridge_name", req.BridgeName, "error", err)
		return fmt.Errorf("failed to get bridge '%s': %w", req.BridgeName, err)
	}

	// Check if bridge is connected
	if !bridge.IsConnected() {
		return fmt.Errorf("bridge '%s' is not connected", req.BridgeName)
	}

	// Check if the bridge channel is already mapped. This is a reverse lookup
	// (bridge channel -> Mattermost channel), so it must not use GetChannelMapping.
	existingChannelID, err := bridge.GetChannelMappingForBridge(req.BridgeName, req.BridgeChannelID)
	if err != nil {
		m.logger.LogError("Failed to check channel mapping", "bridge_channel_id", req.BridgeChannelID, "error", err)
		return fmt.Errorf("failed to check channel mapping: %w", err)
	}
	if existingChannelID != "" {
		m.logger.LogWarn("Channel already mapped to another channel",
			"bridge_channel_id", req.BridgeChannelID,
			"existing_channel_id", existingChannelID,
			"requested_channel_id", req.ChannelID)
		return fmt.Errorf("channel '%s' is already mapped to channel '%s'", req.BridgeChannelID, existingChannelID)
	}

	// NEW: Check if room exists on target bridge
	channelExists, err := bridge.ChannelMappingExists(req.BridgeChannelID)
	if err != nil {
		m.logger.LogError("Failed to check channel existence", "bridge_channel_id", req.BridgeChannelID, "error", err)
		return fmt.Errorf("failed to check channel existence: %w", err)
	}
	if !channelExists {
		m.logger.LogWarn("Channel does not exist on bridge",
			"bridge_channel_id", req.BridgeChannelID,
			"bridge_name", req.BridgeName)
		return fmt.Errorf("channel '%s' does not exist on %s bridge", req.BridgeChannelID, req.BridgeName)
	}

	m.logger.LogDebug("Channel validation passed",
		"bridge_channel_id", req.BridgeChannelID,
		"bridge_name", req.BridgeName,
		"channel_exists", channelExists,
		"already_mapped", false)

	// Create the channel mapping on the receiving bridge
	if err = bridge.CreateChannelMapping(req.ChannelID, req.BridgeChannelID); err != nil {
		m.logger.LogError("Failed to create channel mapping", "channel_id", req.ChannelID, "bridge_name", req.BridgeName, "bridge_channel_id", req.BridgeChannelID, "error", err)
		return fmt.Errorf("failed to create channel mapping for bridge '%s': %w", req.BridgeName, err)
	}

	mattermostBridge, err := m.GetBridge("mattermost")
	if err != nil {
		m.logger.LogError("Failed to get Mattermost bridge", "error", err)
		return fmt.Errorf("failed to get Mattermost bridge: %w", err)
	}

	// Create the channel mapping in the Mattermost bridge
	if err = mattermostBridge.CreateChannelMapping(req.ChannelID, req.BridgeChannelID); err != nil {
		m.logger.LogError("Failed to create channel mapping in Mattermost bridge", "channel_id", req.ChannelID, "bridge_name", req.BridgeName, "bridge_channel_id", req.BridgeChannelID, "error", err)
		return fmt.Errorf("failed to create channel mapping in Mattermost bridge: %w", err)
	}

	// Share the channel using Mattermost's shared channels API. Without this the
	// channel never syncs, so roll the mappings back and report the failure.
	if err = m.shareChannel(req); err != nil {
		m.logger.LogError("Failed to share channel", "channel_id", req.ChannelID, "bridge_channel_id", req.BridgeChannelID, "error", err)
		if rbErr := bridge.DeleteChannelMapping(req.ChannelID); rbErr != nil {
			m.logger.LogError("Failed to roll back bridge channel mapping", "channel_id", req.ChannelID, "bridge_name", req.BridgeName, "error", rbErr)
		}
		if rbErr := mattermostBridge.DeleteChannelMapping(req.ChannelID); rbErr != nil {
			m.logger.LogError("Failed to roll back Mattermost channel mapping", "channel_id", req.ChannelID, "error", rbErr)
		}
		return fmt.Errorf("failed to share channel: %w", err)
	}

	m.logger.LogInfo("Successfully created channel mapping", "channel_id", req.ChannelID, "bridge_name", req.BridgeName, "bridge_channel_id", req.BridgeChannelID)
	return nil
}

// DeleteChannepMapping handles the deletion of a channel mapping by calling the appropriate bridges
func (m *BridgeManager) DeleteChannepMapping(req model.DeleteChannelMappingRequest) error {
	// Validate request
	if err := req.Validate(); err != nil {
		return fmt.Errorf("invalid delete request: %w", err)
	}

	m.logger.LogDebug("Deleting channel mapping", "channel_id", req.ChannelID, "bridge_name", req.BridgeName, "user_id", req.UserID, "team_id", req.TeamID)

	// Get the specific bridge
	bridge, err := m.GetBridge(req.BridgeName)
	if err != nil {
		m.logger.LogError("Failed to get bridge", "bridge_name", req.BridgeName, "error", err)
		return fmt.Errorf("failed to get bridge '%s': %w", req.BridgeName, err)
	}

	// Check if bridge is connected
	if !bridge.IsConnected() {
		return fmt.Errorf("bridge '%s' is not connected", req.BridgeName)
	}

	// Delete the channel mapping from the specific bridge
	if err = bridge.DeleteChannelMapping(req.ChannelID); err != nil {
		m.logger.LogError("Failed to delete channel mapping", "channel_id", req.ChannelID, "bridge_name", req.BridgeName, "error", err)
		return fmt.Errorf("failed to delete channel mapping for bridge '%s': %w", req.BridgeName, err)
	}

	// Also delete from Mattermost bridge to clean up reverse mappings
	mattermostBridge, err := m.GetBridge("mattermost")
	if err != nil {
		m.logger.LogError("Failed to get Mattermost bridge", "error", err)
		return fmt.Errorf("failed to get Mattermost bridge: %w", err)
	}

	// Delete the channel mapping from the Mattermost bridge
	if err = mattermostBridge.DeleteChannelMapping(req.ChannelID); err != nil {
		m.logger.LogError("Failed to delete channel mapping from Mattermost bridge", "channel_id", req.ChannelID, "bridge_name", req.BridgeName, "error", err)
		return fmt.Errorf("failed to delete channel mapping from Mattermost bridge: %w", err)
	}

	// Unshare the channel using Mattermost's shared channels API
	if err = m.unshareChannel(req.ChannelID); err != nil {
		m.logger.LogError("Failed to unshare channel", "channel_id", req.ChannelID, "error", err)
		// Don't fail the entire operation if unsharing fails, but log the error
		m.logger.LogWarn("Channel mapping deleted but unsharing failed - channel may still appear as shared")
	}

	m.logger.LogInfo("Successfully deleted channel mapping", "channel_id", req.ChannelID, "bridge_name", req.BridgeName)
	return nil
}

// shareChannel creates a shared channel configuration using the Mattermost API
func (m *BridgeManager) shareChannel(req *model.CreateChannelMappingRequest) error {
	if m.remoteID == "" {
		return fmt.Errorf("remote ID not set - plugin not registered for shared channels")
	}

	// Create SharedChannel configuration
	sharedChannel := &mmModel.SharedChannel{
		ChannelId:        req.ChannelID,
		TeamId:           req.TeamID,
		Home:             true,
		ReadOnly:         false,
		ShareName:        model.SanitizeShareName(fmt.Sprintf("bridge-%s", req.BridgeChannelID)),
		ShareDisplayName: fmt.Sprintf("Bridge: %s", req.BridgeChannelID),
		SharePurpose:     fmt.Sprintf("Shared channel bridged to %s", req.BridgeChannelID),
		ShareHeader:      "test header",
		CreatorId:        req.UserID,
		RemoteId:         "",
	}

	// Share the channel
	sharedChannel, err := m.api.ShareChannel(sharedChannel)
	if err != nil {
		return fmt.Errorf("failed to share channel via API: %w", err)
	}

	// ShareChannel only marks the channel as shared; nothing syncs until a remote is
	// invited to it. Invite this plugin's own remote so the sync service starts
	// delivering posts to OnSharedChannelsSyncMsg.
	if err = m.api.InviteRemoteToChannel(req.ChannelID, m.remoteID, req.UserID, false); err != nil {
		return fmt.Errorf("failed to invite bridge remote to channel: %w", err)
	}

	m.logger.LogInfo("Successfully shared channel", "channel_id", req.ChannelID, "shared_channel_id", sharedChannel.ChannelId, "remote_id", m.remoteID)
	return nil
}

// unshareChannel removes shared channel configuration using the Mattermost API
func (m *BridgeManager) unshareChannel(channelID string) error {
	// Unshare the channel
	unshared, err := m.api.UnshareChannel(channelID)
	if err != nil {
		return fmt.Errorf("failed to unshare channel via API: %w", err)
	}

	if !unshared {
		m.logger.LogWarn("Channel was not shared or already unshared", "channel_id", channelID)
	} else {
		m.logger.LogInfo("Successfully unshared channel", "channel_id", channelID)
	}

	return nil
}

// startBridgeMessageHandler starts message handling for a specific bridge
func (m *BridgeManager) startBridgeMessageHandler(bridgeID string, bridge model.Bridge) {
	m.logger.LogDebug("Starting message handler for bridge", "bridge_id", bridgeID)

	// Subscribe to message bus
	messageChannel := m.messageBus.Subscribe(bridgeID)

	// Start message routing goroutine
	m.routingWg.Go(func() {
		defer m.logger.LogDebug("Message handler stopped for bridge", "bridge_id", bridgeID)

		for {
			select {
			case msg, ok := <-messageChannel:
				if !ok {
					m.logger.LogDebug("Message channel closed for bridge", "bridge_id", bridgeID)
					return
				}

				if err := m.handleBridgeMessage(bridgeID, bridge, msg); err != nil {
					m.logger.LogError("Failed to handle message for bridge",
						"bridge_id", bridgeID,
						"source_bridge", msg.SourceBridge,
						"error", err)
				}

			case <-m.routingCtx.Done():
				m.logger.LogDebug("Context cancelled, stopping message handler", "bridge_id", bridgeID)
				return
			}
		}
	})

	// Listen to bridge's outgoing messages
	m.routingWg.Go(func() {
		defer m.logger.LogDebug("Bridge message listener stopped", "bridge_id", bridgeID)

		bridgeMessageChannel := bridge.GetMessageChannel()
		for {
			select {
			case msg, ok := <-bridgeMessageChannel:
				if !ok {
					m.logger.LogDebug("Bridge message channel closed", "bridge_id", bridgeID)
					return
				}

				if err := m.messageBus.Publish(msg); err != nil {
					m.logger.LogError("Failed to publish message from bridge",
						"bridge_id", bridgeID,
						"direction", msg.Direction,
						"error", err)
				}

			case <-m.routingCtx.Done():
				m.logger.LogDebug("Context cancelled, stopping bridge listener", "bridge_id", bridgeID)
				return
			}
		}
	})
}

// handleBridgeMessage processes an incoming message for a specific bridge
func (m *BridgeManager) handleBridgeMessage(bridgeID string, bridge model.Bridge, msg *model.DirectionalMessage) error {
	m.logger.LogDebug("Handling message for bridge",
		"target_bridge", bridgeID,
		"source_bridge", msg.SourceBridge,
		"direction", msg.Direction,
		"channel_id", msg.SourceChannelID)

	// Get the bridge's message handler
	handler := bridge.GetMessageHandler()
	if handler == nil {
		return fmt.Errorf("bridge %s does not have a message handler", bridgeID)
	}

	// Check if the handler can process this message
	if !handler.CanHandleMessage(msg.BridgeMessage) {
		m.logger.LogDebug("Bridge cannot handle message",
			"bridge_id", bridgeID,
			"message_type", msg.MessageType)
		return nil // Not an error, just skip
	}

	// Process the message
	return handler.ProcessMessage(msg)
}

// StartMessageRouting starts the message bus and routing system
func (m *BridgeManager) StartMessageRouting() error {
	m.logger.LogInfo("Starting message routing system")
	return m.messageBus.Start()
}

// StopMessageRouting stops the message bus and routing system
func (m *BridgeManager) StopMessageRouting() error {
	m.logger.LogInfo("Stopping message routing system")

	// Cancel routing context
	if m.routingCancel != nil {
		m.routingCancel()
	}

	// Wait for all routing goroutines to finish
	m.routingWg.Wait()

	// Stop the message bus
	return m.messageBus.Stop()
}

// PublishMessage publishes a message to the message bus for routing to target bridges
func (m *BridgeManager) PublishMessage(msg *model.DirectionalMessage) error {
	m.logger.LogDebug("Publishing message to message bus",
		"source_bridge", msg.SourceBridge,
		"direction", msg.Direction,
		"target_bridges", msg.TargetBridges,
		"message_id", msg.MessageID)

	return m.messageBus.Publish(msg)
}
