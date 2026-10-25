package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent/provider"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/tools/files"
)

// Explicit opt-in keeps normal tests offline and never borrows ambient credentials.
func TestLiveModelCorrectsToolFailure(t *testing.T) {
	if os.Getenv("CYBER_FEEDBACK_LIVE") != "1" {
		t.Skip("set CYBER_FEEDBACK_LIVE=1 with CYBER_API_KEY, CYBER_MODEL and CYBER_BASE_URL")
	}
	for _, key := range []string{"CYBER_API_KEY", "CYBER_MODEL", "CYBER_BASE_URL"} {
		if os.Getenv(key) == "" {
			t.Fatalf("%s is required", key)
		}
	}
	kind := os.Getenv("CYBER_PROVIDER")
	if kind == "" {
		kind = "openai"
	}
	model, err := provider.NewProvider(&provider.ProviderConfig{Provider: kind, APIKey: os.Getenv("CYBER_API_KEY"), Model: os.Getenv("CYBER_MODEL"), BaseURL: os.Getenv("CYBER_BASE_URL"), Timeout: 60})
	if err != nil {
		t.Fatal(err)
	}
	resource, err := files.New(files.Config{Directory: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = resource.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer resource.Close(context.Background())
	if err = resource.Files.Write(t.Context(), "correct.txt", []byte("feedback-recovery-148-173")); err != nil {
		t.Fatal(err)
	}
	tools, err := resource.Files.Tools()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	result, err := NewAgent(Config{Loop: StandardLoop{}, Provider: model, Tools: newTestTools(t, tools...), Model: os.Getenv("CYBER_MODEL"), MaxTurns: 6, MaxTokens: 1024, MaxRetries: -1}).Run(ctx, TextInput("First call read on missing.txt. After receiving its failure, correct the path by reading correct.txt. Return the exact contents of correct.txt. Do not create any files."))
	if err != nil {
		t.Fatal(err)
	}
	failed, recovered := false, false
	for _, message := range result.Messages {
		r := provider.MessageToolResult(message)
		if r == nil {
			continue
		}
		if r.IsError && strings.Contains(coretool.ResultText(r), "missing.txt") {
			failed = true
		}
		if failed && !r.IsError && strings.Contains(coretool.ResultText(r), "feedback-recovery-148-173") {
			recovered = true
		}
	}
	if !failed || !recovered || !strings.Contains(result.Output, "feedback-recovery-148-173") {
		t.Fatalf("model did not correct the failure: failed=%v recovered=%v", failed, recovered)
	}
}
