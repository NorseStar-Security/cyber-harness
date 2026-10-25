package tool

import (
	"strings"

	aop "github.com/chainreactors/cyber/aop"
)

func ResultText(r *aop.ToolResult) string {
	if r == nil {
		return ""
	}
	var sb strings.Builder
	for _, block := range r.Output {
		if text := block.GetText(); text != nil {
			sb.WriteString(text.Text)
		}
	}
	return sb.String()
}

func ResultHasImages(r *aop.ToolResult) bool {
	return resultHasMedia(r, "image")
}

func ResultHasMedia(r *aop.ToolResult) bool {
	return resultHasMedia(r, "")
}

func resultHasMedia(r *aop.ToolResult, kind string) bool {
	if r == nil {
		return false
	}
	for _, block := range r.Output {
		if media := block.GetMedia(); media != nil && (kind == "" || media.Kind == kind) {
			return true
		}
	}
	return false
}

func TextResult(s string) *aop.ToolResult {
	return &aop.ToolResult{Output: []*aop.Content{aop.Text(s)}}
}

func ErrorResult(msg string) *aop.ToolResult {
	return &aop.ToolResult{Output: []*aop.Content{aop.Text(msg)}, IsError: true}
}

func TerminateResult(s string) *aop.ToolResult {
	return &aop.ToolResult{Output: []*aop.Content{aop.Text(s)}, Terminate: true}
}
