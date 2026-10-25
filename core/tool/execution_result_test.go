package tool_test

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	aop "github.com/chainreactors/cyber/aop"
	coretool "github.com/chainreactors/cyber/core/tool"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
)

func TestFeedbackPreservesPartialOutputAndExplainsFailure(t *testing.T) {
	r := &aop.ToolResult{Output: []*aop.Content{aop.Text("partial output"), aop.Image("image/png", []byte("image"))}, CallId: "call", DurationMs: 42}
	err := errors.New("file not found")
	r = toolhooks.Feedback(r, err)
	r = toolhooks.Feedback(r, err)
	if !r.IsError || r.Terminate || r.CallId != "call" || r.DurationMs != 42 || len(r.Output) != 3 || r.Output[1].GetMedia() == nil || !strings.Contains(coretool.ResultText(r), "file not found") {
		t.Fatalf("result=%+v", r)
	}
}

func TestFeedbackUsesStandardErrorChain(t *testing.T) {
	cause := &fs.PathError{Op: "read", Path: "missing.txt", Err: fs.ErrNotExist}
	err := fmt.Errorf("read the correct path before retrying: %w", cause)
	result := toolhooks.Feedback(nil, err)
	var pathError *fs.PathError
	if !errors.Is(err, fs.ErrNotExist) || !errors.As(err, &pathError) || pathError != cause {
		t.Fatal("error chain was lost")
	}
	if !result.IsError || result.Terminate || coretool.ResultText(result) != err.Error() {
		t.Fatalf("feedback=%+v", result)
	}
	if toolhooks.Feedback(result, nil) != result {
		t.Fatal("nil error replaced result")
	}
}
