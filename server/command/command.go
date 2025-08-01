package command

import (
	"fmt"
	"strings"

	pluginModel "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

type Handler struct {
	client        *pluginapi.Client
	bridgeManager pluginModel.BridgeManager
}

type Command interface {
	Handle(args *model.CommandArgs) (*model.CommandResponse, error)
	executeXMPPBridgeCommand(args *model.CommandArgs) *model.CommandResponse
}

const xmppBridgeCommandTrigger = "xmppbridge"

// Register all your slash commands in the NewCommandHandler function.
func NewCommandHandler(client *pluginapi.Client, bridgeManager pluginModel.BridgeManager) Command {
	// Register XMPP bridge command
	xmppBridgeData := model.NewAutocompleteData(xmppBridgeCommandTrigger, "", "Manage XMPP bridge")
	mapSubcommand := model.NewAutocompleteData("map", "[room_jid]", "Map current channel to XMPP room")
	mapSubcommand.AddTextArgument("XMPP room JID (e.g., room@conference.example.com)", "[room_jid]", "")
	xmppBridgeData.AddCommand(mapSubcommand)

	unmapSubcommand := model.NewAutocompleteData("unmap", "", "Unmap current channel from XMPP room")
	xmppBridgeData.AddCommand(unmapSubcommand)

	statusSubcommand := model.NewAutocompleteData("status", "", "Show bridge connection status")
	xmppBridgeData.AddCommand(statusSubcommand)

	err := client.SlashCommand.Register(&model.Command{
		Trigger:          xmppBridgeCommandTrigger,
		AutoComplete:     true,
		AutoCompleteDesc: "Manage XMPP bridge mappings",
		AutoCompleteHint: "[map|unmap|status]",
		AutocompleteData: xmppBridgeData,
	})
	if err != nil {
		client.Log.Error("Failed to register XMPP bridge command", "error", err)
	}

	return &Handler{
		client:        client,
		bridgeManager: bridgeManager,
	}
}

// ExecuteCommand hook calls this method to execute the commands that were registered in the NewCommandHandler function.
func (c *Handler) Handle(args *model.CommandArgs) (*model.CommandResponse, error) {
	trigger := strings.TrimPrefix(strings.Fields(args.Command)[0], "/")
	switch trigger {
	case xmppBridgeCommandTrigger:
		return c.executeXMPPBridgeCommand(args), nil
	default:
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("Unknown command: %s", args.Command),
		}, nil
	}
}

func (c *Handler) executeXMPPBridgeCommand(args *model.CommandArgs) *model.CommandResponse {
	fields := strings.Fields(args.Command)
	if len(fields) < 2 {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text: `### XMPP Bridge Commands

**Available commands:**
- ` + "`/xmppbridge map <room_jid>`" + ` - Map current channel to XMPP room
- ` + "`/xmppbridge unmap`" + ` - Unmap current channel from XMPP room
- ` + "`/xmppbridge status`" + ` - Show bridge connection status

**Example:**
` + "`/xmppbridge map general@conference.example.com`",
		}
	}

	subcommand := fields[1]
	switch subcommand {
	case "map":
		return c.executeMapCommand(args, fields)
	case "unmap":
		return c.executeUnmapCommand(args)
	case "status":
		return c.executeStatusCommand(args)
	default:
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("Unknown subcommand: %s. Use `/xmppbridge` for help.", subcommand),
		}
	}
}

func (c *Handler) executeMapCommand(args *model.CommandArgs, fields []string) *model.CommandResponse {
	if len(fields) < 3 {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "Please specify an XMPP room JID. Example: `/xmppbridge map general@conference.example.com`",
		}
	}

	roomJID := fields[2]
	channelID := args.ChannelId

	// Validate room JID format (basic validation)
	if !strings.Contains(roomJID, "@") {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "Invalid room JID format. Please use format: `room@conference.server.com`",
		}
	}

	// Get the XMPP bridge to check existing mappings
	bridge, err := c.bridgeManager.GetBridge("xmpp")
	if err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ XMPP bridge is not available. Please check the plugin configuration.",
		}
	}

	// Check if bridge is connected
	if !bridge.IsConnected() {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ XMPP bridge is not connected. Please check the plugin configuration.",
		}
	}

	// Check if channel is already mapped
	existingMapping, err := bridge.GetChannelMapping(channelID)
	if err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("Error checking existing mapping: %v", err),
		}
	}

	if existingMapping != "" {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("❌ This channel is already mapped to XMPP room: `%s`", existingMapping),
		}
	}

	// Create the mapping using BridgeManager
	err = c.bridgeManager.OnChannelMappingCreated(channelID, "xmpp", roomJID)
	if err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("❌ Failed to create channel mapping: %v", err),
		}
	}

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         fmt.Sprintf("✅ Successfully mapped this channel to XMPP room: `%s`", roomJID),
	}
}

func (c *Handler) executeUnmapCommand(args *model.CommandArgs) *model.CommandResponse {
	channelID := args.ChannelId

	// Get the XMPP bridge to check existing mappings
	bridge, err := c.bridgeManager.GetBridge("xmpp")
	if err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ XMPP bridge is not available. Please check the plugin configuration.",
		}
	}

	// Check if channel is mapped
	roomJID, err := bridge.GetChannelMapping(channelID)
	if err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("Error checking existing mapping: %v", err),
		}
	}

	if roomJID == "" {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ This channel is not mapped to any XMPP room.",
		}
	}

	// Delete the mapping
	err = c.bridgeManager.OnChannelMappingDeleted(channelID, "xmpp")
	if err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("❌ Failed to unmap channel: %v", err),
		}
	}

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         fmt.Sprintf("✅ Successfully unmapped this channel from XMPP room: `%s`", roomJID),
	}
}

func (c *Handler) executeStatusCommand(args *model.CommandArgs) *model.CommandResponse {
	// Get the XMPP bridge
	bridge, err := c.bridgeManager.GetBridge("xmpp")
	if err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ XMPP bridge is not available. Please check the plugin configuration.",
		}
	}

	isConnected := bridge.IsConnected()

	var statusText string
	if isConnected {
		statusText = "✅ **Connected** - XMPP bridge is active"
	} else {
		statusText = "❌ **Disconnected** - XMPP bridge is not connected"
	}

	// Check if current channel is mapped
	channelID := args.ChannelId
	roomJID, err := bridge.GetChannelMapping(channelID)

	var mappingText string
	if err != nil {
		mappingText = fmt.Sprintf("⚠️ Error checking channel mapping: %v", err)
	} else if roomJID != "" {
		mappingText = fmt.Sprintf("🔗 **Current channel mapping:** `%s`", roomJID)
	} else {
		mappingText = "📝 **Current channel:** Not mapped to any XMPP room"
	}

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text: fmt.Sprintf(`### XMPP Bridge Status

%s

%s

**Commands:**
- Use `+"`/xmppbridge map <room_jid>`"+` to map this channel to an XMPP room
- Use `+"`/xmppbridge unmap`"+` to unmap this channel from an XMPP room`, statusText, mappingText),
	}
}
