package command

import (
	"fmt"
	"strings"

	pluginModel "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

type Handler struct {
	client        *pluginapi.Client
	api           plugin.API
	bridgeManager pluginModel.BridgeManager
}

type Command interface {
	Handle(args *model.CommandArgs) (*model.CommandResponse, error)
	executeXMPPBridgeCommand(args *model.CommandArgs) *model.CommandResponse
}

const xmppBridgeCommandTrigger = "xmppbridge"

// Register all your slash commands in the NewCommandHandler function.
func NewCommandHandler(client *pluginapi.Client, api plugin.API, bridgeManager pluginModel.BridgeManager) Command {
	// Register XMPP bridge command
	xmppBridgeData := model.NewAutocompleteData(xmppBridgeCommandTrigger, "", "Manage XMPP bridge")
	mapSubcommand := model.NewAutocompleteData("map", "[room_jid]", "Map current channel to XMPP room")
	mapSubcommand.AddTextArgument("XMPP room JID (e.g., room@conference.example.com)", "[room_jid]", "")
	xmppBridgeData.AddCommand(mapSubcommand)

	unmapSubcommand := model.NewAutocompleteData("unmap", "", "Unmap current channel from XMPP room")
	xmppBridgeData.AddCommand(unmapSubcommand)

	statusSubcommand := model.NewAutocompleteData("status", "", "Show bridge connection status")
	xmppBridgeData.AddCommand(statusSubcommand)

	syncSubcommand := model.NewAutocompleteData("sync", "", "Force sync with shared channels if channel is mapped")
	xmppBridgeData.AddCommand(syncSubcommand)

	syncResetSubcommand := model.NewAutocompleteData("sync-reset", "", "Reset sync cursor for channel if mapped")
	xmppBridgeData.AddCommand(syncResetSubcommand)

	err := client.SlashCommand.Register(&model.Command{
		Trigger:          xmppBridgeCommandTrigger,
		AutoComplete:     true,
		AutoCompleteDesc: "Manage XMPP bridge mappings",
		AutoCompleteHint: "[map|unmap|status|sync|sync-reset]",
		AutocompleteData: xmppBridgeData,
	})
	if err != nil {
		client.Log.Error("Failed to register XMPP bridge command", "error", err)
	}

	return &Handler{
		client:        client,
		api:           api,
		bridgeManager: bridgeManager,
	}
}

// ExecuteCommand hook calls this method to execute the commands that were registered in the NewCommandHandler function.
func (c *Handler) Handle(args *model.CommandArgs) (*model.CommandResponse, error) {
	// Check if user is system admin for all plugin commands
	if !c.isSystemAdmin(args.UserId) {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ Only system administrators can use XMPP bridge commands.",
		}, nil
	}

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
- ` + "`/xmppbridge sync`" + ` - Force sync with shared channels if channel is mapped
- ` + "`/xmppbridge sync-reset`" + ` - Reset sync cursor for channel if mapped

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
	case "sync":
		return c.executeSyncCommand(args)
	case "sync-reset":
		return c.executeSyncResetCommand(args)
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
	mappingReq := pluginModel.CreateChannelMappingRequest{
		ChannelID:       channelID,
		BridgeName:      "xmpp",
		BridgeChannelID: roomJID,
		UserID:          args.UserId,
		TeamID:          args.TeamId,
	}

	err = c.bridgeManager.CreateChannelMapping(mappingReq)
	if err != nil {
		return c.formatMappingError("create", roomJID, err)
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
	deleteReq := pluginModel.DeleteChannelMappingRequest{
		ChannelID:  channelID,
		BridgeName: "xmpp",
		UserID:     args.UserId,
		TeamID:     args.TeamId,
	}

	err = c.bridgeManager.DeleteChannepMapping(deleteReq)
	if err != nil {
		return c.formatMappingError("delete", roomJID, err)
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

// isSystemAdmin checks if the user is a system administrator
func (c *Handler) isSystemAdmin(userID string) bool {
	user, err := c.client.User.Get(userID)
	if err != nil {
		c.client.Log.Warn("Failed to get user for admin check", "user_id", userID, "error", err)
		return false
	}

	return user.IsSystemAdmin()
}

// formatMappingError provides user-friendly error messages for mapping operations
func (c *Handler) executeSyncCommand(args *model.CommandArgs) *model.CommandResponse {
	channelID := args.ChannelId

	// Get the XMPP bridge to check if channel is mapped
	bridge, err := c.bridgeManager.GetBridge("xmpp")
	if err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ XMPP bridge is not available. Please check the plugin configuration.",
		}
	}

	// Check if channel is mapped to XMPP
	roomJID, err := bridge.GetChannelMapping(channelID)
	if err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("Error checking channel mapping: %v", err),
		}
	}

	if roomJID == "" {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ This channel is not mapped to any XMPP room. Use `/xmppbridge map <room_jid>` to create a mapping first.",
		}
	}

	// Force sync with shared channels
	if err := c.api.SyncSharedChannel(channelID); err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("❌ Failed to sync channel: %v", err),
		}
	}

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         fmt.Sprintf("✅ Successfully triggered sync for channel mapped to XMPP room: `%s`", roomJID),
	}
}

func (c *Handler) executeSyncResetCommand(args *model.CommandArgs) *model.CommandResponse {
	channelID := args.ChannelId

	// Get the XMPP bridge to check if channel is mapped
	bridge, err := c.bridgeManager.GetBridge("xmpp")
	if err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ XMPP bridge is not available. Please check the plugin configuration.",
		}
	}

	// Check if channel is mapped to XMPP
	roomJID, err := bridge.GetChannelMapping(channelID)
	if err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("Error checking channel mapping: %v", err),
		}
	}

	if roomJID == "" {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ This channel is not mapped to any XMPP room. Use `/xmppbridge map <room_jid>` to create a mapping first.",
		}
	}

	// Get remoteID from bridge for cursor operations
	remoteID := bridge.GetRemoteID()
	if remoteID == "" {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ Bridge remote ID not available. Cannot reset sync cursor.",
		}
	}

	// Create empty cursor to reset to beginning
	emptyCursor := model.GetPostsSinceForSyncCursor{
		LastPostUpdateAt: 1,
		LastPostCreateAt: 1,
	}

	// Reset sync cursor using UpdateSharedChannelCursor
	if err := c.api.UpdateSharedChannelCursor(channelID, remoteID, emptyCursor); err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("❌ Failed to reset sync cursor: %v", err),
		}
	}

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         fmt.Sprintf("✅ Successfully reset sync cursor for channel mapped to XMPP room: `%s`", roomJID),
	}
}

func (c *Handler) formatMappingError(operation, roomJID string, err error) *model.CommandResponse {
	errorMsg := err.Error()

	// Handle specific error cases with user-friendly messages
	switch {
	case strings.Contains(errorMsg, "already mapped to channel"):
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text: fmt.Sprintf(`❌ **Channel Already Mapped**

The XMPP room **%s** is already connected to another channel.

**What you can do:**
- Choose a different XMPP room that isn't already in use
- Unmap the room from the other channel first using `+"`/xmppbridge unmap`"+`
- Use `+"`/xmppbridge status`"+` to check current mappings`, roomJID),
		}

	case strings.Contains(errorMsg, "does not exist"):
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text: fmt.Sprintf(`❌ **Channel Not Found**

The XMPP room **%s** doesn't exist or isn't accessible.

**What you can do:**
- Check that the room JID is spelled correctly
- Make sure the room exists on the XMPP server
- Verify you have permission to access the room
- Contact your XMPP administrator if needed

**Example format:** room@conference.example.com`, roomJID),
		}

	case strings.Contains(errorMsg, "not connected"):
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text: `❌ **Bridge Not Connected**

The XMPP bridge is currently disconnected.

**What you can do:**
- Wait a moment and try again (the bridge may be reconnecting)
- Contact your system administrator
- Use ` + "`/xmppbridge status`" + ` to check the connection status`,
		}

	default:
		// Generic error message for unknown cases
		action := "create the mapping"
		if operation == "delete" {
			action = "remove the mapping"
		}

		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text: fmt.Sprintf(`❌ **Operation Failed**

Unable to %s for room **%s**.

**What you can do:**
- Try the command again in a few moments
- Use `+"`/xmppbridge status`"+` to check the bridge status
- Contact your system administrator if the problem persists

**Error details:** %s`, action, roomJID, errorMsg),
		}
	}
}
