package agent

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	provider "github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/pkg/testutil/hosttest"
	"github.com/chainreactors/cyber/tools/files"
	"github.com/chainreactors/cyber/tools/terminal"
)

func TestBasicToolFailureAllowsModelCorrection(t *testing.T) {
	for _, tc := range []struct{ name, tool, args string }{
		{"missing-file", "read", `{"path":"missing.txt"}`},
		{"missing-skill", "read", `{"path":"cyber://skills/missing/SKILL.md"}`},
		{"invalid-json", "read", `{`},
		{"invalid-write", "write", `{"path":"new.txt"}`},
		{"failed-edit", "write", `{"path":"existing.txt","edits":[{"old_text":"absent","new_text":"replacement"}]}`},
		{"missing-directory", "ls", `{"path":"missing-directory"}`},
		{"invalid-glob", "glob", `{"pattern":"["}`},
		{"tmux-missing-session", "bash", `{"command":"tmux capture-pane -t missing-session"}`},
		{"tmux-invalid-arguments", "bash", `{"command":"tmux new-session -s"}`},
		{"partial-output-error", "partial", `{}`},
		{"tool-panic", "panic", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resource, e := files.New(files.Config{Directory: t.TempDir(), Mounts: map[string]fs.FS{"cyber://skills/": fstest.MapFS{"test/SKILL.md": &fstest.MapFile{Data: []byte("skill instructions")}}}}, nil)
			if e != nil {
				t.Fatal(e)
			}
			if e := resource.Open(t.Context()); e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { resource.Close(context.Background()) })
			if e := resource.Files.Write(t.Context(), "existing.txt", []byte("original")); e != nil {
				t.Fatal(e)
			}
			tools, e := resource.Files.Tools()
			if e != nil {
				t.Fatal(e)
			}
			bash := terminal.NewBashTool(t.TempDir(), 5, nil)
			t.Cleanup(bash.Close)
			bash.SetCommandRegistry(hosttest.Commands(t, terminal.NewTmuxCommand(bash)))
			tools = append(tools, bash)
			tools = append(tools, &feedbackFailureTool{name: "partial"}, &feedbackFailureTool{name: "panic"})
			turn := 0
			llm := &callbackProvider{fn: func(_ context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
				turn++
				if turn == 1 {
					return chatResponse(ChatMessage{Role: "assistant", ToolCalls: []ToolCall{{ID: "failed-call", Type: "function", Function: FunctionCall{Name: tc.tool, Arguments: tc.args}}}}), nil
				}
				last := req.Messages[len(req.Messages)-1]
				var result *aop.ToolResult
				for _, c := range last.Content {
					if r := c.GetToolResult(); r != nil {
						result = r
					}
				}
				if result == nil {
					t.Fatalf("no feedback on turn %d", turn)
				}
				if turn == 2 {
					if !result.IsError || result.Terminate || strings.TrimSpace(coretool.ResultText(result)) == "" {
						t.Fatalf("invalid failure feedback: %+v", result)
					}
					if tc.tool == "partial" && !strings.Contains(coretool.ResultText(result), "operation failed after partial output") {
						t.Fatalf("partial output hid failure: %+v", result)
					}
					return chatResponse(ChatMessage{Role: "assistant", ToolCalls: []ToolCall{{ID: "corrected-call", Type: "function", Function: FunctionCall{Name: "read", Arguments: `{"path":"cyber://skills/test/SKILL.md"}`}}}}), nil
				}
				if result.IsError || coretool.ResultText(result) != "skill instructions" {
					t.Fatalf("correction failed: %+v", result)
				}
				return chatResponse(NewTextMessage("assistant", "recovered")), nil
			}}
			result, e := NewAgent(Config{Loop: StandardLoop{}, Provider: llm, Tools: newTestTools(t, tools...), Model: "test"}).Run(t.Context(), TextInput("use tools"))
			if e != nil || result == nil || turn != 3 {
				t.Fatalf("loop did not recover: turns=%d result=%+v err=%v", turn, result, e)
			}
		})
	}
}

type feedbackFailureTool struct{ name string }

func (t *feedbackFailureTool) Name() string        { return t.name }
func (t *feedbackFailureTool) Description() string { return "failure feedback fixture" }
func (t *feedbackFailureTool) Definition() *aop.ToolDefinition {
	return coretool.Def(t.name, t.Description(), struct{}{})
}
func (t *feedbackFailureTool) Execute(context.Context, string) (*aop.ToolResult, error) {
	if t.name == "panic" {
		panic("private panic detail")
	}
	return coretool.TextResult("partial output"), fmt.Errorf("operation failed after partial output")
}

func TestFailureCauseSurvivesModelTruncation(t *testing.T) {
	original := &aop.ToolResult{IsError: true, CallId: "failed", Output: []*aop.Content{aop.Text(strings.Repeat("partial output\n", 100)), aop.Image("image/png", []byte("image")), aop.Text("missing.txt: read correct.txt before retrying")}}
	projected := provider.MessageToolResult(modelToolResultMessage(original, 128))
	if !strings.Contains(coretool.ResultText(projected), "read correct.txt") || !projected.IsError || projected.CallId != "failed" || len(projected.Output) != 2 || projected.Output[1].GetMedia() == nil {
		t.Fatalf("projection=%v", projected)
	}
	if len(coretool.ResultText(original)) < 1000 {
		t.Fatal("canonical result mutated")
	}
}
