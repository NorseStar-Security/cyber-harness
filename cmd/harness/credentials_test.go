package harness_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSharedLLMEnvironmentIsExplicitlyOptedIn(t *testing.T) {
	for _, suffix := range []string{"API_KEY", "BASE_URL", "MODEL", "PROVIDER"} {
		t.Setenv("CYBER_"+suffix, "shared-"+suffix)
	}
	for _, include := range []bool{false, true} {
		env := strings.Join(testEnvironment(include), "\n")
		for _, suffix := range []string{"API_KEY", "BASE_URL", "MODEL", "PROVIDER"} {
			if strings.Contains(env, "CYBER_"+suffix+"=shared-"+suffix) != include {
				t.Fatalf("include=%v suffix=%s", include, suffix)
			}
		}
	}
}

func TestRedactionCoversLLMAndJEVKeys(t *testing.T) {
	t.Setenv("CYBER_API_KEY", `shared-"key`)
	t.Setenv("TYPESAFE_API_KEY", "jev-test-key")
	encoded, _ := json.Marshal(`shared-"key`)
	got := string(redactSecrets([]byte(`shared-"key jev-test-key ` + string(encoded))))
	if strings.Contains(got, "shared-") || strings.Contains(got, "jev-test-key") || strings.Count(got, "[REDACTED]") != 3 {
		t.Fatalf("redaction failed: %s", got)
	}
}
