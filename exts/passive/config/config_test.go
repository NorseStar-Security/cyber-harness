package config

import (
	"testing"

	cfg "github.com/chainreactors/cyber/pkg/config"
)

func resolveSections(t *testing.T, file, cli cfg.Values, env map[string]string) *cfg.Resolved {
	t.Helper()
	registry := cfg.NewSections()
	if _, err := registry.Add(ReconSection()); err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.ResolveValues(file, cli, func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestReconSectionEnvironmentAndExplicitLimit(t *testing.T) {
	resolved := resolveSections(t,
		nil,
		cfg.Values{ReconConfigKey: map[string]any{"fofa_key": "cli-key", "limit": 0}},
		map[string]string{"FOFA_KEY": "env-key", "HUNTER_API_KEY": "hunter"},
	)
	value, err := cfg.Get[*ReconOptions](resolved, ReconConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	if value.FofaKey != "cli-key" {
		t.Fatalf("fofa_key = %q, want CLI over env", value.FofaKey)
	}
	if value.HunterAPIKey != "hunter" {
		t.Fatalf("hunter_api_key = %q", value.HunterAPIKey)
	}
	if value.Limit == nil || *value.Limit != 0 {
		t.Fatalf("explicit zero limit lost: %#v", value.Limit)
	}
}

func TestReconSectionPreservesExplicitZeroLimitFromFile(t *testing.T) {
	resolved := resolveSections(t, cfg.Values{ReconConfigKey: map[string]any{"limit": 0}}, nil, nil)
	value, err := cfg.Get[*ReconOptions](resolved, ReconConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	if value.Limit == nil || *value.Limit != 0 {
		t.Fatalf("file explicit zero limit lost: %#v", value.Limit)
	}
}

func TestUncoverCredentialsFromEnvironment(t *testing.T) {
	values := map[string]string{"SHODAN_API_KEY": "shodan-key", "CENSYS_API_TOKEN": "  "}
	credentials := UncoverCredentials(func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	})
	if len(credentials) != 1 || credentials["SHODAN_API_KEY"] != "shodan-key" {
		t.Fatalf("credentials = %#v", credentials)
	}
	if UncoverCredentials(nil) != nil {
		t.Fatal("nil lookup must yield nil credentials")
	}
}
