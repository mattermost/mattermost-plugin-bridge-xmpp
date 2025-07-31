package main

import (
	"reflect"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/config"
	"github.com/pkg/errors"
)

// getConfiguration retrieves the active configuration under lock, making it safe to use
// concurrently. The active configuration may change underneath the client of this method, but
// the struct returned by this API call is considered immutable.
func (p *Plugin) getConfiguration() *config.Configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()

	if p.configuration == nil {
		return &config.Configuration{}
	}

	return p.configuration
}

// setConfiguration replaces the active configuration under lock.
//
// Do not call setConfiguration while holding the configurationLock, as sync.Mutex is not
// reentrant. In particular, avoid using the plugin API entirely, as this may in turn trigger a
// hook back into the plugin. If that hook attempts to acquire this lock, a deadlock may occur.
//
// This method panics if setConfiguration is called with the existing configuration. This almost
// certainly means that the configuration was modified without being cloned and may result in
// an unsafe access.
func (p *Plugin) setConfiguration(configuration *config.Configuration) {
	p.configurationLock.Lock()
	defer p.configurationLock.Unlock()

	if configuration != nil && p.configuration == configuration {
		// Ignore assignment if the configuration struct is empty. Go will optimize the
		// allocation for same to point at the same memory address, breaking the check
		// above.
		if reflect.ValueOf(*configuration).NumField() == 0 {
			return
		}

		panic("setConfiguration called with the existing configuration")
	}

	p.configuration = configuration
}

// OnConfigurationChange is invoked when configuration changes may have been made.
func (p *Plugin) OnConfigurationChange() error {
	var configuration = new(config.Configuration)

	// Load the public configuration fields from the Mattermost server configuration.
	if err := p.API.LoadPluginConfiguration(configuration); err != nil {
		return errors.Wrap(err, "failed to load plugin configuration")
	}

	p.API.LogDebug("Loaded configuration in OnConfigurationChange", "configuration", configuration)

	// Validate the configuration
	if err := configuration.IsValid(); err != nil {
		p.API.LogError("Configuration validation failed", "error", err, "configuration", configuration)
		return errors.Wrap(err, "invalid plugin configuration")
	}

	p.setConfiguration(configuration)

	// Update bridge configurations (only if bridges have been initialized)
	if p.mattermostToXMPPBridge != nil {
		if err := p.mattermostToXMPPBridge.UpdateConfiguration(configuration); err != nil {
			p.logger.LogWarn("Failed to update Mattermost to XMPP bridge configuration", "error", err)
		}
	}

	return nil
}
