package main

import "testing"

func TestProviderDefaults(t *testing.T) {
	c := defaultConfig()
	if c.Provider != "opencode-go" || c.Model != "deepseek-v4.1-flash" {
		t.Fatalf("defaults are %q / %q", c.Provider, c.Model)
	}
	if p := providerFor(""); p.ID != "opencode-go" || p.Env != "OPENCODE_API_KEY" {
		t.Errorf("providerFor(\"\") = %+v", p)
	}
	if key := providerSecretKey("opencode-go"); key != "pi-key-opencode-go" {
		t.Errorf("secret key %q", key)
	}
}
