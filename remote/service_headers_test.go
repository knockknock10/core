// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithHeaders_ServiceDefaultsAndRequestOverride(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient(server.URL, WithHeaders(map[string]string{
		"X-Default":  "service",
		"X-Override": "service",
	}))

	err := client.Request(context.Background(), Request{
		Method: http.MethodGet,
		Path:   "/",
		Headers: map[string]string{
			"X-Override": "request",
			"X-Request":  "request-only",
		},
	}, nil)
	require.NoError(t, err)

	assert.Equal(t, "service", got.Get("X-Default"))
	assert.Equal(t, "request", got.Get("X-Override"))
	assert.Equal(t, "request-only", got.Get("X-Request"))
}

func TestWithHeaders_CopiesInput(t *testing.T) {
	headers := map[string]string{"X-Test": "before"}
	client := NewClient("http://example.test", WithHeaders(headers))

	headers["X-Test"] = "after"
	headers["X-New"] = "mutated"

	assert.Equal(t, "before", client.headers["X-Test"])
	assert.NotContains(t, client.headers, "X-New")
}

func TestManager_LoadServices_ResolvesHeaderSecretRefs(t *testing.T) {
	t.Setenv("TEST_SERVICE_API_KEY", "secret-value")

	body := "{"version":"1.0","services":[{"id":"svc","url":"http://local","headers":{"X-API-Key":"env:TEST_SERVICE_API_KEY","X-Version":"literal:v2"}}]}"
	path := filepath.Join(t.TempDir(), "services.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	manager := NewManager()
	require.NoError(t, manager.LoadServices(path))

	client, err := manager.GetClient("svc")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"X-API-Key": "secret-value",
		"X-Version": "v2",
	}, client.headers)
}

func TestManager_LoadServices_RejectsAuthHeaderCollision(t *testing.T) {
	body := "{"version":"1.0","services":[{"id":"svc","url":"http://local","headers":{"authorization":"static"},"auth":{"type":"bearer","options":{"token":"token"}}}]}"
	path := filepath.Join(t.TempDir(), "services.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	err := NewManager().LoadServices(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicts with the authentication header")
}
