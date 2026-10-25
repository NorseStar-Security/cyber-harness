package hooks

import (
	"strings"

	"github.com/chainreactors/cyber/aop"
)

// Feedback projects an execution error into the existing model-visible result.
// It preserves partial output, media and explicit control flags. The caller
// retains the original error; applying the projection again is harmless.
func Feedback(result *aop.ToolResult, err error) *aop.ToolResult {
	if result == nil {
		result = &aop.ToolResult{}
	}
	if err == nil {
		return result
	}
	result.IsError = true
	message := strings.ToValidUTF8(err.Error(), "\uFFFD")
	if strings.TrimSpace(message) == "" {
		message = "Tool execution failed."
	}
	var text strings.Builder
	for _, block := range result.Output {
		text.WriteString(block.GetText().GetText())
	}
	if !strings.Contains(text.String(), message) {
		if text.Len() > 0 {
			message = "\n" + message
		}
		result.Output = append(result.Output, aop.Text(message))
	}
	return result
}
