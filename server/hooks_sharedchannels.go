package main

import "github.com/mattermost/mattermost/server/public/model"

// OnSharedChannelsPing is called to check if the bridge is healthy and ready to process messages
func (p *Plugin) OnSharedChannelsPing(remoteCluster *model.RemoteCluster) bool {
	config := p.getConfiguration()

	p.logger.LogDebug("OnSharedChannelsPing called", "remote_cluster_id", remoteCluster.RemoteId)

	var remoteClusterID string
	if remoteCluster != nil {
		remoteClusterID = remoteCluster.RemoteId
	}

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
