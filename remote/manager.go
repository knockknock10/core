// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/OpenNSW/core/remote/auth"
	"github.com/OpenNSW/core/secret"
)

type AuthConfig struct {
	Type    string          `json:"type"` // "api_key", "oauth2", "bearer"
	Options json.RawMessage `json:"options"`
}

// TLSSettings configures transport-level client authentication (mTLS) for a
// service. Both values are filesystem paths to PEM files, not secret
// references: certificate chains routinely exceed the 4 KB cap that
// secret.SecretRef places on file-sourced secrets.
type TLSSettings struct {
	ClientCertFile string `json:"client_cert_file"`
	ClientKeyFile  string `json:"client_key_file"`
}

type ServiceConfig struct {
	ID      string                      `json:"id"`
	URL     string                      `json:"url"`
	Timeout string                      `json:"timeout"`
	Headers map[string]secret.SecretRef `json:"headers,omitempty"`
	Auth    *AuthConfig                 `json:"auth,omitempty"`
	TLS     *TLSSettings                `json:"tls,omitempty"`
}

type Registry struct {
	Version  string          `json:"version"`
	Services []ServiceConfig `json:"services"`
}

type Manager struct {
	mu             sync.RWMutex
	configs        map[string]ServiceConfig
	clients        map[string]*Client
	authenticators map[string]auth.Authenticator
	headers        map[string]map[string]string
}

func NewManager() *Manager {
	return &Manager{
		configs:        make(map[string]ServiceConfig),
		clients:        make(map[string]*Client),
		authenticators: make(map[string]auth.Authenticator),
		headers:        make(map[string]map[string]string),
	}
}

func (m *Manager) LoadServices(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("remote: failed to read services file: %w", err)
	}

	var registry Registry
	if err := json.Unmarshal(data, &registry); err != nil {
		return fmt.Errorf("remote: failed to unmarshal services registry: %w", err)
	}

	// Prepare the new state outside the lock: this performs I/O (resolving secret
	// references in auth.Build) without blocking concurrent readers, and ensures a
	// failure leaves the manager's existing state untouched rather than corrupted.
	configs := make(map[string]ServiceConfig, len(registry.Services))
	authenticators := make(map[string]auth.Authenticator)
	resolvedHeaders := make(map[string]map[string]string, len(registry.Services))
	for _, cfg := range registry.Services {
		// Normalize URL by removing trailing slash for consistent matching
		cfg.URL = strings.TrimSuffix(cfg.URL, "/")

		// Build authenticators eagerly so secret references resolve once, now,
		// and any misconfiguration (unset env var, unreadable file) fails loud
		// at startup rather than on the first request.
		if cfg.Auth != nil {
			authenticator, err := auth.Build(cfg.Auth.Type, cfg.Auth.Options)
			if err != nil {
				return fmt.Errorf("remote: failed to configure auth for service %q: %w", cfg.ID, err)
			}
			authenticators[cfg.ID] = authenticator
		}

		headers, err := resolveServiceHeaders(cfg)
		if err != nil {
			return fmt.Errorf("remote: failed to configure headers for service %q: %w", cfg.ID, err)
		}
		resolvedHeaders[cfg.ID] = headers
		configs[cfg.ID] = cfg

		// Validate the tls config shape now, but defer reading the PEM files to
		// the first client build (GetClient): mTLS material is operator-supplied
		// and often absent in dev setups, and deferring lets it be provided
		// after boot without failing startup for every other service.
		if cfg.TLS != nil && (cfg.TLS.ClientCertFile == "" || cfg.TLS.ClientKeyFile == "") {
			return fmt.Errorf("remote: service %q tls config requires both client_cert_file and client_key_file", cfg.ID)
		}
	}

	// Swap in the validated state atomically, and reset the client cache.
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients = make(map[string]*Client)
	m.configs = configs
	m.authenticators = authenticators
	m.headers = resolvedHeaders

	slog.Info("remote: services loaded", "file", filePath, "count", len(configs))
	return nil
}

func resolveServiceHeaders(cfg ServiceConfig) (map[string]string, error) {
	if len(cfg.Headers) == 0 {
		return nil, nil
	}

	reserved, err := authHeaderName(cfg.Auth)
	if err != nil {
		return nil, err
	}

	resolved := make(map[string]string, len(cfg.Headers))
	for name, ref := range cfg.Headers {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("header name cannot be empty")
		}
		if reserved != "" && strings.EqualFold(name, reserved) {
			return nil, fmt.Errorf("header %q conflicts with the authentication header %q", name, reserved)
		}
		value, err := ref.Resolve()
		if err != nil {
			return nil, fmt.Errorf("header %q: %w", name, err)
		}
		resolved[name] = value
	}
	return resolved, nil
}

func authHeaderName(cfg *AuthConfig) (string, error) {
	if cfg == nil {
		return "", nil
	}
	switch cfg.Type {
	case "bearer", "oauth2":
		return "Authorization", nil
	case "api_key":
		var options struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(cfg.Options, &options); err != nil {
			return "", fmt.Errorf("invalid api_key options: %w", err)
		}
		if strings.TrimSpace(options.Key) == "" {
			return "", fmt.Errorf("api_key options require a non-empty key")
		}
		return options.Key, nil
	default:
		return "", nil
	}
}

func (m *Manager) Call(ctx context.Context, serviceID string, req Request, response interface{}) error {
	client, err := m.GetClient(serviceID)
	if err != nil {
		return err
	}

	return client.Request(ctx, req, response)
}

// CallRaw sends a raw-bodied request (e.g. a SOAP/XML envelope, via
// req.Body = RawBody{...}) to a registered service. See Client.RawRequest for
// the error semantics: a non-2xx status is returned in the response, not as
// an error.
func (m *Manager) CallRaw(ctx context.Context, serviceID string, req Request) (*RawResponse, error) {
	client, err := m.GetClient(serviceID)
	if err != nil {
		return nil, err
	}
	return client.RawRequest(ctx, req)
}

func (m *Manager) GetClient(id string) (*Client, error) {
	m.mu.RLock()
	client, ok := m.clients[id]
	m.mu.RUnlock()

	if ok {
		return client, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if client, ok := m.clients[id]; ok {
		return client, nil
	}

	cfg, ok := m.configs[id]
	if !ok {
		return nil, fmt.Errorf("remote: service %q not found in registry", id)
	}

	var opts []Option

	if cfg.Timeout != "" {
		d, err := time.ParseDuration(cfg.Timeout)
		if err != nil {
			return nil, fmt.Errorf("remote: invalid timeout %q for service %q: %w", cfg.Timeout, id, err)
		}
		opts = append(opts, WithTimeout(d))
	}

	// Authenticators are built and resolved once, at load time (see LoadServices).
	if authenticator, ok := m.authenticators[id]; ok {
		opts = append(opts, WithAuthenticator(authenticator))
	}

	if headers := m.headers[id]; len(headers) > 0 {
		opts = append(opts, WithHeaders(headers))
	}

	// The client certificate is read from disk on each TLS handshake (see
	// WithClientCertificateFiles): a missing PEM fails the call, not startup,
	// and rotated material is picked up by new connections without a restart.
	if cfg.TLS != nil {
		opts = append(opts, WithClientCertificateFiles(cfg.TLS.ClientCertFile, cfg.TLS.ClientKeyFile))
	}

	newClient := NewClient(cfg.URL, opts...)
	m.clients[id] = newClient

	return newClient, nil
}

func (m *Manager) ListServices() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.configs))
	for id := range m.configs {
		ids = append(ids, id)
	}
	return ids
}
