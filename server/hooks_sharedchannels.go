package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	pluginModel "github.com/mattermost/mattermost-plugin-bridge-xmpp/server/model"
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

// OnSharedChannelsSyncMsg processes sync messages from Mattermost shared channels and routes them to XMPP.
//
// Posts that fail individually are listed in PostErrors so the server retries just those posts.
// An error is returned only when nothing was delivered and the whole batch should be retried.
func (p *Plugin) OnSharedChannelsSyncMsg(msg *model.SyncMsg, rc *model.RemoteCluster) (model.SyncResponse, error) {
	var remoteClusterID string
	if rc != nil {
		remoteClusterID = rc.RemoteId
	}

	config := p.getConfiguration()

	var response model.SyncResponse

	p.logger.LogDebug("OnSharedChannelsSyncMsg called",
		"remote_cluster_id", remoteClusterID,
		"channel_id", msg.ChannelId,
		"post_count", len(msg.Posts))

	// If sync is disabled, return success but don't process
	if !config.EnableSync {
		p.logger.LogDebug("Sync message received but sync is disabled", "remote_cluster_id", remoteClusterID)
		return response, nil
	}

	if len(msg.Posts) == 0 {
		return response, nil
	}

	if err := p.checkXMPPConnected(); err != nil {
		p.logger.LogWarn("XMPP unavailable, failing sync message",
			"remote_cluster_id", remoteClusterID,
			"channel_id", msg.ChannelId,
			"error", err)
		return model.SyncResponse{}, err
	}

	var processedCount int
	var delivered bool

	for i, post := range msg.Posts {
		published, err := p.processSyncPost(post, msg.ChannelId, msg.Users)
		if err == nil {
			processedCount++
			delivered = delivered || published
			continue
		}

		p.logger.LogError("Failed to process sync post", "post_id", post.Id, "error", err)

		if errors.Is(err, errMessageBus) {
			if !delivered {
				return model.SyncResponse{}, err
			}
			for _, remaining := range msg.Posts[i:] {
				response.PostErrors = append(response.PostErrors, remaining.Id)
			}
			break
		}

		response.PostErrors = append(response.PostErrors, post.Id)
	}

	p.logger.LogInfo("Processed sync message",
		"remote_cluster_id", remoteClusterID,
		"channel_id", msg.ChannelId,
		"processed_posts", processedCount,
		"failed_posts", len(response.PostErrors))

	return response, nil
}

func (p *Plugin) checkXMPPConnected() error {
	if p.bridgeManager == nil {
		return errors.New("bridge manager not initialized")
	}

	bridge, err := p.bridgeManager.GetBridge("xmpp")
	if err != nil {
		return fmt.Errorf("XMPP bridge not available: %w", err)
	}

	if !bridge.IsConnected() {
		return errors.New("XMPP bridge is not connected")
	}

	return nil
}

var errMessageBus = errors.New("message bus unavailable")

// processSyncPost converts a Mattermost post to a bridge message and routes it to XMPP.
// It reports whether the post was published, as opposed to skipped for loop prevention.
func (p *Plugin) processSyncPost(post *model.Post, channelID string, users map[string]*model.User) (bool, error) {
	p.logger.LogDebug("Processing sync post", "post_id", post.Id, "channel_id", channelID)

	// Skip messages from our own bot user to prevent loops
	if post.UserId == p.botUserID {
		p.logger.LogDebug("Skipping message from bot user to prevent loop",
			"bot_user_id", p.botUserID,
			"post_user_id", post.UserId)
		return false, nil
	}

	// Skip messages from remote users to prevent loops
	// Remote users represent users from other bridges (e.g., XMPP users in Mattermost)
	user, appErr := p.API.GetUser(post.UserId)
	if appErr != nil && appErr.StatusCode == http.StatusNotFound {
		p.logger.LogWarn("Skipping sync post whose user does not exist", "post_id", post.Id, "user_id", post.UserId)
		return false, nil
	}
	if appErr != nil {
		return false, fmt.Errorf("failed to get user %s for loop prevention: %w", post.UserId, appErr)
	}
	if user == nil {
		return false, fmt.Errorf("user %s not found for loop prevention", post.UserId)
	}
	if user.RemoteId != nil && *user.RemoteId != "" {
		p.logger.LogDebug("Skipping message from remote user to prevent loop",
			"user_id", post.UserId,
			"username", user.Username,
			"remote_id", *user.RemoteId)
		return false, nil
	}

	// Find the user who created this post
	var postUser *model.User
	if users != nil {
		postUser = users[post.UserId]
	}

	if postUser == nil {
		postUser = user
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
		return false, fmt.Errorf("%w: %w", errMessageBus, err)
	}

	p.logger.LogDebug("Successfully published sync message to message bus",
		"post_id", post.Id,
		"channel_id", channelID,
		"user", postUser.Username)

	return true, nil
}
