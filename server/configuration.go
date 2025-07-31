package main

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/pkg/errors"
)

const DefaultXMPPUsernamePrefix = "xmpp"

// configuration captures the plugin's external configuration as exposed in the Mattermost server
// configuration, as well as values computed from the configuration. Any public fields will be
// deserialized from the Mattermost server configuration in OnConfigurationChange.
//
// As plugins are inherently concurrent (hooks being called asynchronously), and the plugin
// configuration can change at any time, access to the configuration must be synchronized. The
// strategy used in this plugin is to guard a pointer to the configuration, and clone the entire
// struct whenever it changes. You may replace this with whatever strategy you choose.
//
// If you add non-reference types to your configuration struct, be sure to rewrite Clone as a deep
// copy appropriate for your types.
type configuration struct {
	XMPPServerURL      string `json:"xmpp_server_url"`
	XMPPUsername       string `json:"xmpp_username"`
	XMPPPassword       string `json:"xmpp_password"`
	EnableSync         bool   `json:"enable_sync"`
	XMPPUsernamePrefix string `json:"xmpp_username_prefix"`
	XMPPResource       string `json:"xmpp_resource"`
}

// Clone shallow copies the configuration. Your implementation may require a deep copy if
// your configuration has reference types.
func (c *configuration) Clone() *configuration {
	var clone = *c
	return &clone
}

// GetXMPPUsernamePrefix returns the configured username prefix, or the default if not set
func (c *configuration) GetXMPPUsernamePrefix() string {
	if c.XMPPUsernamePrefix == "" {
		return DefaultXMPPUsernamePrefix
	}
	return c.XMPPUsernamePrefix
}

// GetXMPPResource returns the configured XMPP resource, or a default if not set
func (c *configuration) GetXMPPResource() string {
	if c.XMPPResource == "" {
		return "mattermost-bridge"
	}
	return c.XMPPResource
}

// IsValid validates the configuration and returns an error if invalid
func (c *configuration) IsValid() error {
	if c.EnableSync {
		if c.XMPPServerURL == "" {
			return fmt.Errorf("XMPP Server URL is required when sync is enabled")
		}
		if c.XMPPUsername == "" {
			return fmt.Errorf("XMPP Username is required when sync is enabled")
		}
		if c.XMPPPassword == "" {
			return fmt.Errorf("XMPP Password is required when sync is enabled")
		}
		
		// Validate server URL format
		if !strings.Contains(c.XMPPServerURL, ":") {
			return fmt.Errorf("XMPP Server URL must include port (e.g., server.com:5222)")
		}
		
		// Validate username prefix doesn't contain invalid characters
		prefix := c.GetXMPPUsernamePrefix()
		if strings.ContainsAny(prefix, ":@/\\") {
			return fmt.Errorf("XMPP Username Prefix cannot contain special characters (:, @, /, \\)")
		}
	}
	
	return nil
}

// getConfiguration retrieves the active configuration under lock, making it safe to use
// concurrently. The active configuration may change underneath the client of this method, but
// the struct returned by this API call is considered immutable.
func (p *Plugin) getConfiguration() *configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()

	if p.configuration == nil {
		return &configuration{}
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
func (p *Plugin) setConfiguration(configuration *configuration) {
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
	var configuration = new(configuration)

	// Load the public configuration fields from the Mattermost server configuration.
	if err := p.API.LoadPluginConfiguration(configuration); err != nil {
		return errors.Wrap(err, "failed to load plugin configuration")
	}

	// Validate the configuration
	if err := configuration.IsValid(); err != nil {
		return errors.Wrap(err, "invalid plugin configuration")
	}

	p.setConfiguration(configuration)

	return nil
}
