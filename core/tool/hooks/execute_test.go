package hooks

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
)

func TestExecutionCompletion(t *testing.T) {
	for _, mode := range []string{"success", "denied", "before-panic", "panic", "error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			r := hooks.New()
			calls, completions := 0, 0
			Before.On(r, "policy", func(ctx context.Context, _ CallEvent) (Admission, error) {
				if mode == "before-panic" {
					panic("private")
				}
				if mode == "denied" {
					return Admission{Deny: errors.New("nope")}, nil
				}
				return Admission{}, nil
			})
			Completed.On(r, "observe", func(ctx context.Context, c Completion) (struct{}, error) {
				completions++
				if !c.StartedAt.IsZero() != (calls != 0) {
					t.Error("incorrect execution status")
				}
				if c.Operation.GetOperationId() == "" || c.Result.CallId == "" {
					t.Error("missing identity")
				}
				if (c.Err == nil) != (mode == "success") {
					t.Errorf("completion error: %v", c.Err)
				}
				return struct{}{}, nil
			})
			result, err := Execute(t.Context(), r, "test", "{}", func(ctx context.Context, _ string) (*aop.ToolResult, error) {
				calls++
				switch mode {
				case "panic":
					panic("private")
				case "error":
					return nil, errors.New("failed")
				case "cancel":
					operation.RequestCancel(ctx, errors.New("policy canceled"))
				}
				return &aop.ToolResult{Output: []*aop.Content{aop.Text("ok")}}, nil
			})
			if completions != 1 {
				t.Fatalf("completions = %d", completions)
			}
			if mode == "denied" || mode == "before-panic" {
				if calls != 0 || !errors.Is(err, operation.ErrDenied) {
					t.Fatalf("calls=%d error=%v", calls, err)
				}
			}
			if result.IsError != (mode != "success") {
				t.Fatal("incorrect terminal error status")
			}
		})
	}
}

func TestAfterPreservesContentAndCannotEraseCancellation(t *testing.T) {
	r := hooks.New()
	After.On(r, "transform", func(_ context.Context, event ResultEvent) (struct{}, error) {
		event.Result.IsError = false
		event.Result.Terminate = true
		return struct{}{}, nil
	})
	want := errors.New("canceled by policy")
	result, err := Execute(t.Context(), r, "test", "{}", func(ctx context.Context, _ string) (*aop.ToolResult, error) {
		operation.RequestCancel(ctx, want)
		return &aop.ToolResult{Output: []*aop.Content{aop.Text("committed before cancellation"), aop.Text("second block")}}, nil
	})
	if !errors.Is(err, want) || !result.IsError || !result.Terminate || len(result.Output) != 3 || result.Output[2].GetText().GetText() != "\ncanceled by policy" {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestBeforeCannotModifyInvocationArguments(t *testing.T) {
	r := hooks.New()
	Before.On(r, "observer", func(_ context.Context, e CallEvent) (Admission, error) {
		e.Call.Arguments.Data[0] = '!'
		e.Call.Name = "other"
		return Admission{}, nil
	})
	_, err := Execute(t.Context(), r, "test", "{}", func(_ context.Context, args string) (*aop.ToolResult, error) {
		if args != "{}" {
			t.Fatal("arguments were mutated")
		}
		return &aop.ToolResult{Output: []*aop.Content{aop.Text("ok")}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCompletionKeepsCauseAndPartialFeedback(t *testing.T) {
	r := hooks.New()
	cause := &fs.PathError{Op: "read", Path: "missing", Err: fs.ErrNotExist}
	want := fmt.Errorf("use correct path: %w", cause)
	Completed.On(r, "observe", func(_ context.Context, c Completion) (struct{}, error) {
		var pathErr *fs.PathError
		if !errors.Is(c.Err, fs.ErrNotExist) || !errors.As(c.Err, &pathErr) || pathErr != cause || !c.Result.IsError || len(c.Result.Output) != 2 || !strings.Contains(c.Result.Output[1].GetText().GetText(), "use correct path") {
			t.Errorf("completion=%v", c)
		}
		return struct{}{}, nil
	})
	_, err := Execute(t.Context(), r, "read", "{}", func(context.Context, string) (*aop.ToolResult, error) {
		return &aop.ToolResult{Output: []*aop.Content{aop.Text("partial")}}, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}
