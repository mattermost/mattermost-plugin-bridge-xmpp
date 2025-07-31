package main

import (
	"net/http"
	"sync"
	"time"

	mattermostbridge "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/bridge/mattermost"
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

	backgroundJob *cluster.Job

	// configurationLock synchronizes access to the configuration.
	configurationLock sync.RWMutex

	// configuration is the active plugin configuration. Consult getConfiguration and
	// setConfiguration for usage.
	configuration *config.Configuration

	// Bridge components for dependency injection architecture
	mattermostToXMPPBridge pluginModel.Bridge
	xmppToMattermostBridge pluginModel.Bridge
}

// OnActivate is invoked when the plugin is activated. If an error is returned, the plugin will be deactivated.
func (p *Plugin) OnActivate() error {
	p.client = pluginapi.NewClient(p.API, p.Driver)

	// Initialize the logger using Mattermost Plugin API
	p.logger = logger.NewPluginAPILogger(p.API)

	p.kvstore = kvstore.NewKVStore(p.client)

	p.initXMPPClient()

	// Load configuration directly
	cfg := new(config.Configuration)
	if err := p.API.LoadPluginConfiguration(cfg); err != nil {
		p.logger.LogWarn("Failed to load plugin configuration during activation", "error", err)
		cfg = &config.Configuration{} // Use empty config as fallback
	}
	p.logger.LogDebug("Loaded configuration in OnActivate", "config", cfg)
	
	// Initialize bridges with current configuration
	p.initBridges(*cfg)

	p.commandClient = command.NewCommandHandler(p.client, p.mattermostToXMPPBridge)

	// Start the bridge
	if err := p.mattermostToXMPPBridge.Start(); err != nil {
		p.logger.LogWarn("Failed to start bridge during activation", "error", err)
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

	if err := p.mattermostToXMPPBridge.Stop(); err != nil {
		p.API.LogError("Failed to stop Mattermost to XMPP bridge", "err", err)
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
	)
}

func (p *Plugin) initBridges(cfg config.Configuration) {
	if p.mattermostToXMPPBridge == nil {
		// Create bridge instances with all dependencies and configuration
		p.mattermostToXMPPBridge = mattermostbridge.NewMattermostToXMPPBridge(
			p.logger,
			p.API,
			p.kvstore,
			&cfg,
		)
		
		p.logger.LogInfo("Bridge instances created successfully")
	}

	// if p.xmppToMattermostBridge == nil {
	// 	p.xmppToMattermostBridge = xmppbridge.NewXMPPToMattermostBridge()
	// }
}

// See https://developers.mattermost.com/extend/plugins/server/reference/
