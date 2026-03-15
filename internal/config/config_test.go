package config

import "testing"

func TestResolveAdminConfig_AllowsMissingBearerTokenEnv(t *testing.T) {
	cfg := &Config{
		Auth: AuthConfig{
			Admin: AdminConfig{
				BearerTokenEnv: "TEST_GATEWAY_ADMIN_TOKEN_OPTIONAL_MISSING",
			},
		},
	}

	if err := resolveAdminConfig(cfg); err != nil {
		t.Fatalf("expected missing admin token env to be allowed, got error: %v", err)
	}
	if cfg.Auth.Admin.BearerToken != "" {
		t.Fatalf("expected empty admin bearer token, got %q", cfg.Auth.Admin.BearerToken)
	}
}
