// Package xmpp provides XEP (XMPP Extension Protocol) feature implementations.
package xmpp

import (
	"sync"

	"github.com/mattermost/mattermost-plugin-bridge-xmpp/server/logger"
)

// XEPHandler defines the interface that all XEP implementations must satisfy
type XEPHandler interface {
	// Namespace returns the XML namespace for this XEP
	Namespace() string

	// Name returns a human-readable name for this XEP
	Name() string
}

// XEPFeatures manages all XEP implementations for an XMPP client
type XEPFeatures struct {
	// XEP-0077: In-Band Registration
	InBandRegistration *InBandRegistration

	logger logger.Logger
	mu     sync.RWMutex
}

// NewXEPFeatures creates a new XEP features manager
func NewXEPFeatures(log logger.Logger) *XEPFeatures {
	return &XEPFeatures{
		logger: log,
	}
}

// ListFeatures returns a list of available XEP feature names
func (x *XEPFeatures) ListFeatures() []string {
	x.mu.RLock()
	defer x.mu.RUnlock()

	var features []string
	if x.InBandRegistration != nil {
		features = append(features, "InBandRegistration")
	}

	return features
}

// GetFeatureByNamespace retrieves a XEP feature by its XML namespace
func (x *XEPFeatures) GetFeatureByNamespace(namespace string) XEPHandler {
	x.mu.RLock()
	defer x.mu.RUnlock()

	if x.InBandRegistration != nil && x.InBandRegistration.Namespace() == namespace {
		return x.InBandRegistration
	}

	return nil
}
