package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/bridge"
	mattermostbridge "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/bridge/mattermost"
	xmppbridge "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/bridge/xmpp"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/command"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/config"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
	pluginModel "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/store/kvstore"
	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/xmpp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"github.com/pkg/errors"
)

// Plugin implements the interface expected by the Mattermost server to communicate between the server and plugin processes.
type Plugin struct {
	plugin.MattermostPlugin

	// kvstore is the client used to read/write KV records for this plugin.
	kvstore kvstore.KVStore

	// client is the Mattermost server API client.
	client *pluginapi.Client

	// commandClient is the client used to register and execute slash commands.
	commandClient command.Command

	// xmppClient is the client used to communicate with XMPP servers.
	xmppClient *xmpp.Client

	// logger is the main plugin logger
	logger logger.Logger

	// remoteID is the identifier returned by RegisterPluginForSharedChannels
	remoteID string

	// botUserID is the ID of the bot user created for this plugin
	botUserID string

	// backgroundJob is the scheduled job that runs periodically to perform background tasks.
	backgroundJob *cluster.Job

	// configurationLock synchronizes access to the configuration.
	configurationLock sync.RWMutex

	// configuration is the active plugin configuration. Consult getConfiguration and
	// setConfiguration for usage.
	configuration *config.Configuration

	// Bridge manager for managing all bridge instances
	bridgeManager pluginModel.BridgeManager
}

// OnActivate is invoked when the plugin is activated. If an error is returned, the plugin will be deactivated.
func (p *Plugin) OnActivate() error {
	p.client = pluginapi.NewClient(p.API, p.Driver)

	// Initialize the logger using Mattermost Plugin API
	p.logger = logger.NewPluginAPILogger(p.API)

	p.kvstore = kvstore.NewKVStore(p.client)

	p.initXMPPClient()

	// Load configuration directly
	cfg := p.getConfiguration()
	p.logger.LogDebug("Loaded configuration in OnActivate", "config", cfg)

	// Register the plugin for shared channels
	if err := p.registerForSharedChannels(); err != nil {
		p.logger.LogError("Failed to register for shared channels", "error", err)
		return fmt.Errorf("failed to register for shared channels: %w", err)
	}

	// Initialize bridge manager
	p.bridgeManager = bridge.NewBridgeManager(p.logger, p.API, p.remoteID)

	// Initialize and register bridges with current configuration
	if err := p.initBridges(*cfg); err != nil {
		p.logger.LogError("Failed to initialize bridges", "error", err)
		return fmt.Errorf("failed to initialize bridges: %w", err)
	}

	p.commandClient = command.NewCommandHandler(p.client, p.bridgeManager)

	// Start all bridges
	for _, bridgeName := range p.bridgeManager.ListBridges() {
		if err := p.bridgeManager.StartBridge(bridgeName); err != nil {
			p.logger.LogWarn("Failed to start bridge during activation", "bridge", bridgeName, "error", err)
		}
	}

	job, err := cluster.Schedule(
		p.API,
		"BackgroundJob",
		cluster.MakeWaitForRoundedInterval(1*time.Hour),
		p.runJob,
	)
	if err != nil {
		return errors.Wrap(err, "failed to schedule background job")
	}

	p.backgroundJob = job

	return nil
}

// OnDeactivate is invoked when the plugin is deactivated.
func (p *Plugin) OnDeactivate() error {
	if p.backgroundJob != nil {
		if err := p.backgroundJob.Close(); err != nil {
			p.API.LogError("Failed to close background job", "err", err)
		}
	}

	if p.bridgeManager != nil {
		if err := p.bridgeManager.Shutdown(); err != nil {
			p.API.LogError("Failed to shutdown bridge manager", "err", err)
		}
	}

	if err := p.API.UnregisterPluginForSharedChannels(manifest.Id); err != nil {
		p.API.LogError("Failed to unregister plugin for shared channels", "err", err)
	}

	return nil
}

// This will execute the commands that were registered in the NewCommandHandler function.
func (p *Plugin) ExecuteCommand(c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	response, err := p.commandClient.Handle(args)
	if err != nil {
		return nil, model.NewAppError("ExecuteCommand", "plugin.command.execute_command.app_error", nil, err.Error(), http.StatusInternalServerError)
	}
	return response, nil
}

func (p *Plugin) initXMPPClient() {
	cfg := p.getConfiguration()
	p.xmppClient = xmpp.NewClient(
		cfg.XMPPServerURL,
		cfg.XMPPUsername,
		cfg.XMPPPassword,
		cfg.GetXMPPResource(),
		p.remoteID,
		p.logger,
	)
}

func (p *Plugin) initBridges(cfg config.Configuration) error {
	// Create and register XMPP bridge
	xmppBridge := xmppbridge.NewBridge(
		p.logger,
		p.API,
		p.kvstore,
		&cfg,
	)

	if err := p.bridgeManager.RegisterBridge("xmpp", xmppBridge); err != nil {
		return fmt.Errorf("failed to register XMPP bridge: %w", err)
	}

	// Create and register Mattermost bridge
	mattermostBridge := mattermostbridge.NewBridge(
		p.logger,
		p.API,
		p.kvstore,
		&cfg,
	)

	if err := p.bridgeManager.RegisterBridge("mattermost", mattermostBridge); err != nil {
		return fmt.Errorf("failed to register Mattermost bridge: %w", err)
	}

	p.logger.LogInfo("Bridge instances created and registered successfully")
	return nil
}

func (p *Plugin) registerForSharedChannels() error {
	botUserID, err := p.API.EnsureBotUser(&model.Bot{
		Username:    "mattermost-bridge",
		DisplayName: "Mattermost Bridge",
		Description: "Mattermost Bridge Bot",
	})
	if err != nil {
		return fmt.Errorf("failed to ensure bot user: %w", err)
	}

	p.botUserID = botUserID

	opts := model.RegisterPluginOpts{
		Displayname:  "XMPP-Bridge",
		PluginID:     manifest.Id,
		CreatorID:    botUserID,
		AutoShareDMs: false,
		AutoInvited:  true,
	}

	remoteID, appErr := p.API.RegisterPluginForSharedChannels(opts)
	if appErr != nil {
		return fmt.Errorf("failed to register plugin for shared channels: %w", appErr)
	}

	// Store the remote ID for use in sync operations
	p.remoteID = remoteID

	p.logger.LogInfo("Successfully registered plugin for shared channels", "remote_id", remoteID)
	return nil
}

// See https://developers.mattermost.com/extend/plugins/server/reference/
