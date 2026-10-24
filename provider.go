package main

import "github.com/egoist/mygo"

// modelInfo is one model OpenCode Go offers, as the settings picker
// shows it.
type modelInfo struct {
	ID   string
	Name string
}

// provider is an AI provider Pi can talk to. OpenCode Go is the one Stilday
// configures today; the list is shaped so more can be added without
// touching the chat.
type provider struct {
	ID           string
	Name         string
	Env          string // the environment variable Pi reads the key from
	DefaultModel string
	KeyHint      string
	// ModelsURL lists the provider's models for the settings picker.
	ModelsURL string
	// CatalogID keys Pi's local model catalog; empty means ID.
	CatalogID string
}

// catalogID is the provider's key in Pi's catalog.
func (p provider) catalogID() string {
	if p.CatalogID != "" {
		return p.CatalogID
	}
	return p.ID
}

var providers = []provider{
	{
		ID:           "opencode-go",
		Name:         "OpenCode Go",
		Env:          "OPENCODE_API_KEY",
		DefaultModel: "deepseek-v4.1-flash",
		KeyHint:      "在 opencode.ai 获取 OpenCode API key",
		ModelsURL:    "https://opencode.ai/inference/go/v1/models",
	},
}

func providerFor(id string) provider {
	for _, p := range providers {
		if p.ID == id {
			return p
		}
	}
	return providers[0]
}

// providerSecretKey is the Keychain key of a provider's API key.
func providerSecretKey(id string) string {
	return "pi-key-" + providerFor(id).ID
}

// appSecrets uses one Keychain service for both `go run`/GoLand builds and
// the ad-hoc .app, whose executable-derived services otherwise differ.
func appSecrets() mygo.SecretsModule {
	return mygo.Secrets.Service("stilday")
}
