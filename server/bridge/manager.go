package bridge

import (
	"fmt"
	"sync"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
	mmModel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// BridgeManager manages multiple bridge instances
type BridgeManager struct {
	bridges  map[string]model.Bridge
	mu       sync.RWMutex
	logger   logger.Logger
	api      plugin.API
	remoteID string
}

// NewBridgeManager creates a new bridge manager
func NewBridgeManager(logger logger.Logger, api plugin.API, remoteID string) model.BridgeManager {
	if logger == nil {
		panic("logger cannot be nil")
	}
	if api == nil {
		panic("plugin API cannot be nil")
	}

	return &BridgeManager{
		bridges:  make(map[string]model.Bridge),
		logger:   logger,
		api:      api,
		remoteID: remoteID,
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
	m.logger.LogInfo("Bridge registered", "name", name)

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

	m.logger.LogInfo("Starting bridge", "name", name)

	if err := bridge.Start(); err != nil {
		m.logger.LogError("Failed to start bridge", "name", name, "error", err)
		return fmt.Errorf("failed to start bridge '%s': %w", name, err)
	}

	m.logger.LogInfo("Bridge started successfully", "name", name)
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

	m.logger.LogInfo("Stopping bridge", "name", name)

	if err := bridge.Stop(); err != nil {
		m.logger.LogError("Failed to stop bridge", "name", name, "error", err)
		return fmt.Errorf("failed to stop bridge '%s': %w", name, err)
	}

	m.logger.LogInfo("Bridge stopped successfully", "name", name)
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
			m.logger.LogWarn("Failed to stop bridge during unregistration", "name", name, "error", err)
		}
	}

	delete(m.bridges, name)
	m.logger.LogInfo("Bridge unregistered", "name", name)

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

	var errors []error
	for name, bridge := range m.bridges {
		if bridge.IsConnected() {
			if err := bridge.Stop(); err != nil {
				errors = append(errors, fmt.Errorf("failed to stop bridge '%s': %w", name, err))
				m.logger.LogError("Failed to stop bridge during shutdown", "name", name, "error", err)
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
func (m *BridgeManager) OnPluginConfigurationChange(config any) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.bridges) == 0 {
		return nil
	}

	m.logger.LogInfo("Plugin configuration changed, propagating to bridges", "bridge_count", len(m.bridges))

	var errors []error
	for name, bridge := range m.bridges {
		if err := bridge.UpdateConfiguration(config); err != nil {
			errors = append(errors, fmt.Errorf("failed to update configuration for bridge '%s': %w", name, err))
			m.logger.LogError("Failed to update bridge configuration", "name", name, "error", err)
		} else {
			m.logger.LogDebug("Successfully updated bridge configuration", "name", name)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("configuration update completed with errors: %v", errors)
	}

	m.logger.LogInfo("Configuration changes propagated to all bridges")
	return nil
}

// CreateChannelMapping handles the creation of a channel mapping by calling the appropriate bridge
func (m *BridgeManager) CreateChannelMapping(req model.CreateChannelMappingRequest) error {
	// Validate request
	if err := req.Validate(); err != nil {
		return fmt.Errorf("invalid mapping request: %w", err)
	}

	m.logger.LogDebug("Creating channel mapping", "channel_id", req.ChannelID, "bridge_name", req.BridgeName, "bridge_room_id", req.BridgeRoomID, "user_id", req.UserID, "team_id", req.TeamID)

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

	// NEW: Check if room already mapped to another channel
	existingChannelID, err := bridge.GetRoomMapping(req.BridgeRoomID)
	if err != nil {
		m.logger.LogError("Failed to check room mapping", "bridge_room_id", req.BridgeRoomID, "error", err)
		return fmt.Errorf("failed to check room mapping: %w", err)
	}
	if existingChannelID != "" {
		m.logger.LogWarn("Room already mapped to another channel",
			"bridge_room_id", req.BridgeRoomID,
			"existing_channel_id", existingChannelID,
			"requested_channel_id", req.ChannelID)
		return fmt.Errorf("room '%s' is already mapped to channel '%s'", req.BridgeRoomID, existingChannelID)
	}

	// NEW: Check if room exists on target bridge
	roomExists, err := bridge.RoomExists(req.BridgeRoomID)
	if err != nil {
		m.logger.LogError("Failed to check room existence", "bridge_room_id", req.BridgeRoomID, "error", err)
		return fmt.Errorf("failed to check room existence: %w", err)
	}
	if !roomExists {
		m.logger.LogWarn("Room does not exist on bridge",
			"bridge_room_id", req.BridgeRoomID,
			"bridge_name", req.BridgeName)
		return fmt.Errorf("room '%s' does not exist on %s bridge", req.BridgeRoomID, req.BridgeName)
	}

	m.logger.LogDebug("Room validation passed",
		"bridge_room_id", req.BridgeRoomID,
		"bridge_name", req.BridgeName,
		"room_exists", roomExists,
		"already_mapped", false)

	// Create the channel mapping on the receiving bridge
	if err = bridge.CreateChannelMapping(req.ChannelID, req.BridgeRoomID); err != nil {
		m.logger.LogError("Failed to create channel mapping", "channel_id", req.ChannelID, "bridge_name", req.BridgeName, "bridge_room_id", req.BridgeRoomID, "error", err)
		return fmt.Errorf("failed to create channel mapping for bridge '%s': %w", req.BridgeName, err)
	}

	mattermostBridge, err := m.GetBridge("mattermost")
	if err != nil {
		m.logger.LogError("Failed to get Mattermost bridge", "error", err)
		return fmt.Errorf("failed to get Mattermost bridge: %w", err)
	}

	// Create the channel mapping in the Mattermost bridge
	if err = mattermostBridge.CreateChannelMapping(req.ChannelID, req.BridgeRoomID); err != nil {
		m.logger.LogError("Failed to create channel mapping in Mattermost bridge", "channel_id", req.ChannelID, "bridge_name", req.BridgeName, "bridge_room_id", req.BridgeRoomID, "error", err)
		return fmt.Errorf("failed to create channel mapping in Mattermost bridge: %w", err)
	}

	// Share the channel using Mattermost's shared channels API
	if err = m.shareChannel(req); err != nil {
		m.logger.LogError("Failed to share channel", "channel_id", req.ChannelID, "bridge_room_id", req.BridgeRoomID, "error", err)
		// Don't fail the entire operation if sharing fails, but log the error
		m.logger.LogWarn("Channel mapping created but sharing failed - channel may not sync properly")
	}

	m.logger.LogInfo("Successfully created channel mapping", "channel_id", req.ChannelID, "bridge_name", req.BridgeName, "bridge_room_id", req.BridgeRoomID)
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
func (m *BridgeManager) shareChannel(req model.CreateChannelMappingRequest) error {
	if m.remoteID == "" {
		return fmt.Errorf("remote ID not set - plugin not registered for shared channels")
	}

	// Create SharedChannel configuration
	sharedChannel := &mmModel.SharedChannel{
		ChannelId:        req.ChannelID,
		TeamId:           req.TeamID,
		Home:             true,
		ReadOnly:         false,
		ShareName:        model.SanitizeShareName(fmt.Sprintf("bridge-%s", req.BridgeRoomID)),
		ShareDisplayName: fmt.Sprintf("Bridge: %s", req.BridgeRoomID),
		SharePurpose:     fmt.Sprintf("Shared channel bridged to %s", req.BridgeRoomID),
		ShareHeader:      "test header",
		CreatorId:        req.UserID,
		RemoteId:         m.remoteID,
	}

	// Share the channel
	sharedChannel, err := m.api.ShareChannel(sharedChannel)
	if err != nil {
		return fmt.Errorf("failed to share channel via API: %w", err)
	}

	m.logger.LogInfo("Successfully shared channel", "channel_id", req.ChannelID, "shared_channel_id", sharedChannel.ChannelId)
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
