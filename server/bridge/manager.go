package bridge

import (
	"fmt"
	"sync"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
)

// Manager manages multiple bridge instances
type Manager struct {
	bridges map[string]model.Bridge
	mu      sync.RWMutex
	logger  logger.Logger
}

// NewManager creates a new bridge manager
func NewManager(logger logger.Logger) model.BridgeManager {
	if logger == nil {
		panic("logger cannot be nil")
	}

	return &Manager{
		bridges: make(map[string]model.Bridge),
		logger:  logger,
	}
}

// RegisterBridge registers a bridge with the manager
func (m *Manager) RegisterBridge(name string, bridge model.Bridge) error {
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
func (m *Manager) StartBridge(name string) error {
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
func (m *Manager) StopBridge(name string) error {
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
func (m *Manager) UnregisterBridge(name string) error {
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
func (m *Manager) GetBridge(name string) (model.Bridge, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bridge, exists := m.bridges[name]
	if !exists {
		return nil, fmt.Errorf("bridge '%s' is not registered", name)
	}

	return bridge, nil
}

// ListBridges returns a list of all registered bridge names
func (m *Manager) ListBridges() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bridges := make([]string, 0, len(m.bridges))
	for name := range m.bridges {
		bridges = append(bridges, name)
	}

	return bridges
}

// HasBridge checks if a bridge with the given name is registered
func (m *Manager) HasBridge(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, exists := m.bridges[name]
	return exists
}

// HasBridges checks if any bridges are registered
func (m *Manager) HasBridges() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.bridges) > 0
}

// Shutdown stops and unregisters all bridges
func (m *Manager) Shutdown() error {
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
func (m *Manager) OnPluginConfigurationChange(config any) error {
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