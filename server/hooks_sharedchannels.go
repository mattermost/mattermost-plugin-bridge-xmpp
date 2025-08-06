package main

import (
	"fmt"
	"time"

	pluginModel "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
	"github.com/mattermost/mattermost/server/public/model"
)

// OnSharedChannelsPing is called to check if the bridge is healthy and ready to process messages
func (p *Plugin) OnSharedChannelsPing(remoteCluster *model.RemoteCluster) bool {
	config := p.getConfiguration()

	var remoteClusterID string
	if remoteCluster != nil {
		remoteClusterID = remoteCluster.RemoteId
	}

	p.logger.LogDebug("OnSharedChannelsPing called", "remote_cluster_id", remoteClusterID)

	p.logger.LogDebug("Received shared channels ping", "remote_cluster_id", remoteClusterID)

	// If sync is disabled, we're still "healthy" but not actively processing
	if !config.EnableSync {
		p.logger.LogDebug("Ping received but sync is disabled", "remote_cluster_id", remoteClusterID)
		return true
	}

	// Check if bridge manager is available
	if p.bridgeManager == nil {
		p.logger.LogError("Bridge manager not initialized during ping", "remote_cluster_id", remoteClusterID)
		return false
	}

	// Get the XMPP bridge for active connectivity testing
	bridge, err := p.bridgeManager.GetBridge("xmpp")
	if err != nil {
		p.logger.LogWarn("XMPP bridge not available during ping", "error", err, "remote_cluster_id", remoteClusterID)
		// Return true if bridge is not registered - this might be expected during startup/shutdown
		return false
	}

	// Perform active ping test on the XMPP bridge
	if err := bridge.Ping(); err != nil {
		p.logger.LogError("XMPP bridge ping failed", "error", err, "remote_cluster_id", remoteClusterID)
		return false
	}

	p.logger.LogDebug("Shared channels ping successful - XMPP bridge is healthy", "remote_cluster_id", remoteClusterID)
	return true
}

// OnSharedChannelsSyncMsg processes sync messages from Mattermost shared channels and routes them to XMPP
func (p *Plugin) OnSharedChannelsSyncMsg(msg *model.SyncMsg, rc *model.RemoteCluster) (model.SyncResponse, error) {
	p.logger.LogDebug("🚀 OnSharedChannelsSyncMsg called", "remote_id", rc.RemoteId, "channel_id", msg.ChannelId)

	config := p.getConfiguration()

	// Initialize sync response
	now := model.GetMillis()
	response := model.SyncResponse{
		PostsLastUpdateAt:     now,
		UsersLastUpdateAt:     now,
		ReactionsLastUpdateAt: now,
	}

	var remoteClusterID string
	if rc != nil {
		remoteClusterID = rc.RemoteId
	}

	p.logger.LogDebug("OnSharedChannelsSyncMsg called",
		"remote_cluster_id", remoteClusterID,
		"channel_id", msg.ChannelId,
		"post_count", len(msg.Posts))

	// If sync is disabled, return success but don't process
	if !config.EnableSync {
		p.logger.LogDebug("Sync message received but sync is disabled", "remote_cluster_id", remoteClusterID)
		return response, nil
	}

	// Process each post in the sync message
	var processedCount int
	var errors []string

	for _, post := range msg.Posts {
		if err := p.processSyncPost(post, msg.ChannelId, msg.Users); err != nil {
			errorMsg := fmt.Sprintf("failed to process post %s: %v", post.Id, err)
			errors = append(errors, errorMsg)
			p.logger.LogError("Failed to process sync post", "post_id", post.Id, "error", err)
		} else {
			processedCount++
		}
	}

	p.logger.LogInfo("Processed sync message",
		"remote_cluster_id", remoteClusterID,
		"channel_id", msg.ChannelId,
		"processed_posts", processedCount,
		"failed_posts", len(errors))

	// If we have errors, return them
	if len(errors) > 0 {
		return response, fmt.Errorf("failed to process %d posts: %v", len(errors), errors)
	}

	return response, nil
}

// processSyncPost converts a Mattermost post to a bridge message and routes it to XMPP
func (p *Plugin) processSyncPost(post *model.Post, channelID string, users map[string]*model.User) error {
	p.logger.LogDebug("Processing sync post", "post_id", post.Id, "channel_id", channelID, "users", users)

	// Skip messages from our own bot user to prevent loops
	if post.UserId == p.botUserID {
		p.logger.LogDebug("Skipping message from bot user to prevent loop",
			"bot_user_id", p.botUserID,
			"post_user_id", post.UserId)
		return nil
	}

	// Skip messages from remote users to prevent loops
	// Remote users represent users from other bridges (e.g., XMPP users in Mattermost)
	user, appErr := p.API.GetUser(post.UserId)
	if appErr != nil {
		p.logger.LogWarn("Failed to get user details for loop prevention. Ignoring message.", "user_id", post.UserId, "error", appErr)
		return nil
	} else if user != nil && user.RemoteId != nil && *user.RemoteId != "" {
		p.logger.LogDebug("Skipping message from remote user to prevent loop",
			"user_id", post.UserId,
			"username", user.Username,
			"remote_id", *user.RemoteId)
		return nil
	}

	// Find the user who created this post
	var postUser *model.User
	p.logger.LogInfo("Processing sync post", "post_id", post.UserId, "users", users)
	if users != nil {
		postUser = users[post.UserId]
	}

	// If user not found in sync data, try to get from API
	if postUser == nil {
		var appErr *model.AppError
		postUser, appErr = p.API.GetUser(post.UserId)
		if appErr != nil {
			p.logger.LogWarn("Failed to get user for post", "user_id", post.UserId, "post_id", post.Id, "error", appErr)
			// Create a placeholder user
			postUser = &model.User{
				Id:       post.UserId,
				Username: "unknown-user",
			}
		}
	}

	// Create bridge message from Mattermost post
	bridgeMessage := &pluginModel.BridgeMessage{
		SourceBridge:    "mattermost",
		SourceChannelID: channelID,
		SourceUserID:    postUser.Id,
		SourceUserName:  postUser.Username,
		SourceRemoteID:  "", // This message comes from Mattermost, so no remote ID
		Content:         post.Message,
		MessageType:     "text", // TODO: Handle other message types
		Timestamp:       time.Unix(post.CreateAt/1000, 0),
		MessageID:       post.Id,
		ThreadID:        post.RootId,
		TargetBridges:   []string{"xmpp"}, // Route to XMPP
		Metadata: map[string]any{
			"original_post": post,
			"channel_id":    channelID,
		},
	}

	// Create directional message for outgoing (Mattermost -> XMPP)
	directionalMessage := &pluginModel.DirectionalMessage{
		BridgeMessage: bridgeMessage,
		Direction:     pluginModel.DirectionOutgoing,
	}

	// Publish the message to the message bus for routing to XMPP bridge
	if err := p.bridgeManager.PublishMessage(directionalMessage); err != nil {
		return fmt.Errorf("failed to publish sync message to message bus: %w", err)
	}

	p.logger.LogDebug("Successfully published sync message to message bus",
		"post_id", post.Id,
		"channel_id", channelID,
		"user", postUser.Username)

	return nil
}
