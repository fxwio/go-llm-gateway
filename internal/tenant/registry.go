package tenant

import (
	"sort"
	"strings"
	"sync"

	"github.com/fxwio/go-llm-gateway/internal/config"
	gatewaymetrics "github.com/fxwio/go-llm-gateway/internal/metrics"
	"github.com/fxwio/go-llm-gateway/pkg/redact"
)

type ClientIdentity struct {
	Token           string
	Fingerprint     string
	Name            string
	Tenant          string
	App             string
	RateLimitQPS    float64
	RateLimitBurst  int
	DailyTokenLimit int64
	Legacy          bool
}

type registryState struct {
	byToken       map[string]ClientIdentity
	byFingerprint map[string]ClientIdentity
	tokens        []ClientIdentity
}

var (
	registryOnce sync.Once
	registryMu   sync.RWMutex
	registry     registryState
)

func ensureRegistry() {
	registryOnce.Do(rebuild)
}

func rebuild() {
	registryMu.Lock()
	defer registryMu.Unlock()

	next := registryState{
		byToken:       make(map[string]ClientIdentity),
		byFingerprint: make(map[string]ClientIdentity),
	}

	cfg := config.GlobalConfig
	if cfg == nil {
		registry = next
		return
	}

	for _, raw := range cfg.Auth.ValidTokens {
		token := strings.TrimSpace(raw)
		if token == "" {
			continue
		}
		fp := redact.TokenFingerprint(token)
		identity := ClientIdentity{
			Token:          token,
			Fingerprint:    fp,
			Name:           "legacy-" + fp,
			Tenant:         "default",
			App:            "legacy",
			RateLimitQPS:   cfg.Auth.RateLimitQPS,
			RateLimitBurst: cfg.Auth.RateLimitBurst,
			Legacy:         true,
		}
		next.byToken[token] = identity
		next.byFingerprint[fp] = identity
		next.tokens = append(next.tokens, identity)
	}

	for _, item := range cfg.Auth.Tokens {
		if item.Disabled {
			continue
		}
		token := strings.TrimSpace(item.Value)
		if token == "" {
			continue
		}
		fp := redact.TokenFingerprint(token)
		identity := ClientIdentity{
			Token:           token,
			Fingerprint:     fp,
			Name:            item.Name,
			Tenant:          defaultString(item.Tenant, "default"),
			App:             defaultString(item.App, "default"),
			RateLimitQPS:    item.RateLimitQPS,
			RateLimitBurst:  item.RateLimitBurst,
			DailyTokenLimit: item.DailyTokenLimit,
			Legacy:          false,
		}
		next.byToken[token] = identity
		next.byFingerprint[fp] = identity
		next.tokens = append(next.tokens, identity)
	}

	sort.Slice(next.tokens, func(i, j int) bool {
		if next.tokens[i].Tenant == next.tokens[j].Tenant {
			if next.tokens[i].App == next.tokens[j].App {
				return next.tokens[i].Name < next.tokens[j].Name
			}
			return next.tokens[i].App < next.tokens[j].App
		}
		return next.tokens[i].Tenant < next.tokens[j].Tenant
	})

	gatewaymetrics.ConfiguredGatewayTokens.Reset()
	for _, token := range next.tokens {
		if token.Legacy {
			continue
		}
		gatewaymetrics.ConfiguredGatewayTokens.WithLabelValues(token.Tenant, token.App, token.Name).Set(1)
	}

	registry = next
}

func ResolveToken(raw string) (ClientIdentity, bool) {
	ensureRegistry()
	registryMu.RLock()
	defer registryMu.RUnlock()
	identity, ok := registry.byToken[strings.TrimSpace(raw)]
	return identity, ok
}

func LookupByFingerprint(fp string) (ClientIdentity, bool) {
	ensureRegistry()
	registryMu.RLock()
	defer registryMu.RUnlock()
	identity, ok := registry.byFingerprint[strings.TrimSpace(fp)]
	return identity, ok
}

func ListTokens() []ClientIdentity {
	ensureRegistry()
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]ClientIdentity, len(registry.tokens))
	copy(out, registry.tokens)
	for i := range out {
		out[i].Token = ""
	}
	return out
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return strings.TrimSpace(v)
}
