package otel

import (
	"context"
	"fmt"
	"time"

	"github.com/chainreactors/cyber/aop"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	"github.com/chainreactors/cyber/core/types"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/encoding/protojson"
)

func (b *aopBridge) consume(event *aop.Event) (resultErr error) {
	if event == nil {
		return nil
	}
	if event.Id != "" {
		key := turnKey{event.SessionId, event.Id}
		if _, seen := b.seen.get(key); seen {
			return nil
		}
		b.seen.put(key, true)
	}
	defer func() { b.eventLog(event, resultErr) }()
	if state := b.sessions[event.SessionId]; state != nil {
		state.last = stamp(event)
	}
	if started := event.GetSessionStarted(); started != nil {
		if b.session(event.SessionId) != nil {
			return nil
		}
		detail, _, err := types.GetDelegation(event)
		if err != nil {
			return err
		}
		state := &session{model: started.Model, parent: started.ParentSessionId, call: started.ParentToolCallId, delegation: detail, emitter: event.Emitter, start: stamp(event), last: stamp(event), ctx: context.Background()}
		if parent, ok := b.calls.get(turnKey{state.parent, state.call}); ok {
			state.ctx = parent
		}
		b.sessions[event.SessionId] = state
		b.sessionStarted(event.SessionId, state)
		return nil
	}
	if ended := event.GetSessionEnded(); ended != nil {
		b.finishSession(event.SessionId, stamp(event))
		state := b.sessions[event.SessionId]
		if state != nil {
			b.sessionEnded(event.SessionId, state, ended.Reason, stamp(event))
			delete(b.sessions, event.SessionId)
		} else {
			state = &session{ctx: context.Background()}
		}
		b.endedSessions.put(event.SessionId, state)
		return nil
	}
	ref := new(operationpb.Ref)
	if _, err := aop.FindTypedExtension(event, ref); err != nil {
		return err
	}
	if handled, err := b.operation(event, ref); handled || err != nil {
		return err
	}
	if handled, err := b.mechanism(event, ref); handled || err != nil {
		return err
	}
	run := b.ensureTurn(event)
	if run == nil {
		if event.GetTurnStarted() == nil && event.GetTurnEnded() == nil {
			b.fact(event, ref)
		}
		return nil
	}
	if event.GetTurnStarted() != nil {
		return nil
	}
	if status := event.GetStatus(); status != nil && status.State == "llm_request" {
		return b.request(event, run)
	}
	if usage := event.GetUsage(); usage != nil {
		if run.llm != nil {
			b.finishRequests(run, usage, stamp(event))
		} else {
			b.fact(event, ref, usageSummary(usage)...)
		}
		return nil
	}
	if message := event.GetMessage(); message != nil {
		if message.Role == "assistant" && run.llm != nil {
			// Empty assistant messages discard a failed streamed attempt.
			if len(message.Content) > 0 {
				run.llm.end = stamp(event)
				for _, content := range message.Content {
					if call := content.GetToolCall(); call != nil && call.Id != "" {
						b.callSources.put(turnKey{event.SessionId, call.Id}, trace.ContextWithSpanContext(context.Background(), run.llm.span.SpanContext()))
					}
				}
			} else {
				run.llm.end = time.Time{}
			}
		}
		if b.capture {
			encoded, err := protojson.Marshal(message)
			if err != nil {
				return err
			}
			content := clip(string(encoded))
			if message.Role == "user" {
				run.input = content
				run.span.SetAttributes(attribute.String("input.value", content))
			}
			if message.Role == "assistant" {
				if len(message.Content) == 0 {
					content = ""
				}
				run.span.SetAttributes(attribute.String("output.value", content))
				if run.llm != nil {
					run.llm.span.SetAttributes(attribute.String("output.value", content))
				}
			}
		}
	}
	if ended := event.GetTurnEnded(); ended != nil {
		if ended.Error != nil || ended.StopReason == "canceled" {
			kind, message := "error", "turn canceled"
			if ended.StopReason == "canceled" {
				kind = "canceled"
			}
			if ended.Error != nil {
				message = ended.Error.Message
			}
			failSpan(run.span, kind, message)
			// Usage follows tool execution. An already observed model response
			// must survive a later tool failure or caller cancellation.
			if run.llm != nil && run.llm.end.IsZero() {
				failSpan(run.llm.span, kind, message)
				run.llm.span.SetAttributes(attribute.String("cyber.stop_reason", ended.StopReason), attribute.Bool("cyber.llm.end_estimated", true))
				run.llm.end = stamp(event)
			}
		}
		b.finishRequests(run, nil, stamp(event))
		// Turn usage summarizes the Agent; it is never billed a second time.
		if ended.Usage != nil {
			run.span.SetAttributes(usageSummary(ended.Usage)...)
		}
		run.span.SetAttributes(attribute.String("cyber.stop_reason", ended.StopReason), attribute.Int64("cyber.context.tokens", int64(ended.ContextTokens)))
		run.span.End(trace.WithTimestamp(stamp(event)))
		key := turnKey{event.SessionId, event.TurnId}
		b.endedTurns.put(key, trace.ContextWithSpanContext(context.Background(), run.span.SpanContext()))
		delete(b.turns, key)
		return nil
	}
	if call := event.GetToolCall(); call != nil {
		b.fact(event, ref, attribute.String("cyber.call.id", call.Id), attribute.String("tool.name", call.Name))
		return nil
	}
	if result := event.GetToolResult(); result != nil {
		b.fact(event, ref, attribute.String("cyber.call.id", result.CallId), attribute.String("tool.name", result.Name), attribute.Bool("cyber.tool.is_error", result.IsError), attribute.Int64("cyber.tool.duration_ms", int64(result.DurationMs)))
		return nil
	}
	b.fact(event, ref)
	return nil
}
func (b *aopBridge) operation(event *aop.Event, ref *operationpb.Ref) (bool, error) {
	payload := event.GetExtension()
	if payload == nil {
		return false, nil
	}
	var kind, name string
	at := stamp(event)
	var completed *operationpb.Completed
	switch {
	case payload.MessageIs(new(operationpb.Started)):
		value := new(operationpb.Started)
		if err := payload.UnmarshalTo(value); err != nil {
			return true, err
		}
		kind, name = value.Kind, value.Name
	case payload.MessageIs(new(operationpb.Completed)):
		completed = new(operationpb.Completed)
		if err := payload.UnmarshalTo(completed); err != nil {
			return true, err
		}
		kind, name = completed.Kind, completed.Name
		if completed.StartedAt != nil {
			at = completed.StartedAt.AsTime()
		}
	default:
		return false, nil
	}
	if ref.OperationId == "" {
		b.fact(event, ref, attribute.Bool("cyber.operation.id_missing", true))
		return true, nil
	}
	key := scopeKey{event.SessionId, "operation", ref.OperationId}
	attrs := []attribute.KeyValue{attribute.String("cyber.operation.id", ref.OperationId), attribute.String("cyber.operation.parent_id", ref.ParentOperationId), attribute.String("cyber.call.id", ref.CallId), attribute.String("cyber.resource.id", ref.ResourceId), attribute.String("cyber.operation.kind", kind), attribute.String("cyber.operation.correlation", ref.Correlation.String())}
	if completed != nil && completed.StartedAt == nil && b.scopes[key] == nil {
		attrs = append(attrs, attribute.Bool("cyber.start_missing", true))
	}
	spanKind := "CHAIN"
	if kind == "tool" {
		spanKind = "TOOL"
		attrs = append(attrs, attribute.String("tool.name", name), attribute.String("gen_ai.operation.name", "execute_tool"), attribute.String("gen_ai.tool.name", name), attribute.String("gen_ai.tool.call.id", ref.CallId))
	}
	spanName := kind + "." + name
	if kind == "tool" {
		spanName = "execute_tool " + name
	}
	state := b.startScope(event, key, spanName, spanKind, at, scopeKey{event.SessionId, "operation", ref.ParentOperationId}, false, attrs...)
	if state == nil {
		return true, nil
	}
	if kind == "tool" && ref.CallId != "" {
		b.calls.put(turnKey{event.SessionId, ref.CallId}, trace.ContextWithSpanContext(context.Background(), state.span.SpanContext()))
	}
	if completed != nil {
		if completed.Failure != nil {
			failSpan(state.span, failureType(completed.Failure.Kind), completed.Failure.Message)
			state.span.SetAttributes(attribute.String("cyber.failure.kind", completed.Failure.Kind.String()))
			state.span.RecordError(fmt.Errorf("%s", completed.Failure.Message), trace.WithTimestamp(stamp(event)))
		}
		b.endScope(key, stamp(event))
	}
	return true, nil
}
func (b *aopBridge) request(event *aop.Event, run *turn) error {
	detail := new(types.LLMRequestDetail)
	if _, err := aop.FindTypedExtension(event, detail); err != nil {
		return err
	}
	run.requests++
	if run.llm != nil {
		// AOP reports cumulative usage for a logical call. Record retry evidence
		// on that call instead of inventing per-attempt duration/token pairs.
		if !run.llm.end.IsZero() {
			run.llm.span.SetAttributes(attribute.Bool("cyber.llm.grouping.uncertain", true))
		}
		run.llm.attempts++
		run.llm.end = time.Time{}
		if b.capture {
			run.llm.span.SetAttributes(attribute.String("output.value", ""))
			run.span.SetAttributes(attribute.String("output.value", ""))
		}
		b.addFact(run.llm.span, event, []attribute.KeyValue{attribute.Int("cyber.llm.attempt", run.llm.attempts)})
		return nil
	}
	model := detail.Model
	if model == "" {
		if state := b.session(event.SessionId); state != nil {
			model = state.model
		}
	}
	attrs := append(attributes(event), attribute.String("openinference.span.kind", "LLM"), attribute.String("gen_ai.operation.name", "chat"), attribute.String("gen_ai.provider.name", b.provider), attribute.Int("cyber.llm.request", run.requests), attribute.Int("cyber.llm.messages", int(detail.Messages)), attribute.Bool("gen_ai.request.stream", detail.Stream), attribute.String("cyber.usage.scope", "logical_request"))
	if model != "" {
		attrs = append(attrs, attribute.String("llm.model_name", model), attribute.String("gen_ai.request.model", model))
	}
	if detail.MaxTokens > 0 {
		attrs = append(attrs, attribute.Int("gen_ai.request.max_tokens", int(detail.MaxTokens)))
	}
	if b.capture && run.input != "" {
		attrs = append(attrs, attribute.String("input.value", run.input), attribute.String("cyber.input.scope", "latest_user_message"))
	}
	name := "chat"
	if model != "" {
		name += " " + model
	}
	ctx, span := b.tracer.Start(run.ctx, name, trace.WithTimestamp(stamp(event)), trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
	run.modelCtx = ctx
	run.llm = &requestSpan{span: span, start: stamp(event), attempts: 1}
	b.addFact(span, event, []attribute.KeyValue{attribute.Int("cyber.llm.attempt", 1)})
	return nil
}
func (b *aopBridge) finishRequests(run *turn, usage *aop.TokenUsage, at time.Time) {
	request := run.llm
	if request == nil {
		return
	}
	end := request.end
	if end.IsZero() {
		end = at
		request.span.SetAttributes(attribute.Bool("cyber.llm.end_estimated", true))
	}
	request.span.SetAttributes(attribute.Int("cyber.llm.observed_attempts", request.attempts))
	if usage != nil {
		setUsage(request.span, usage)
	} else {
		request.span.SetAttributes(attribute.Bool("cyber.usage.missing", true))
		if request.end.IsZero() {
			failSpan(request.span, "incomplete", "request completion was not observed")
			request.span.SetAttributes(attribute.Bool("cyber.incomplete", true))
		}
	}
	endAt(request.span, request.start, end)
	run.llm = nil
}
func failureType(kind operationpb.FailureKind) string {
	switch kind {
	case operationpb.FailureKind_FAILURE_KIND_DENIED:
		return "denied"
	case operationpb.FailureKind_FAILURE_KIND_START_FAILED:
		return "start_failed"
	case operationpb.FailureKind_FAILURE_KIND_CANCELED:
		return "canceled"
	case operationpb.FailureKind_FAILURE_KIND_TIMEOUT:
		return "timeout"
	case operationpb.FailureKind_FAILURE_KIND_PANIC:
		return "panic"
	default:
		return "error"
	}
}
func usageSummary(usage *aop.TokenUsage) []attribute.KeyValue {
	return []attribute.KeyValue{attribute.Int64("cyber.tokens.total", int64(usage.TotalTokens)), attribute.Int64("cyber.tokens.input", int64(usage.InputTokens)), attribute.Int64("cyber.tokens.output", int64(usage.OutputTokens))}
}
func setUsage(span trace.Span, usage *aop.TokenUsage) {
	span.SetAttributes(attribute.Bool("cyber.usage.missing", usage.Detail["usage_missing"] > 0))
	for key, value := range usage.Detail {
		span.SetAttributes(attribute.Int64("cyber.usage.detail."+key, int64(value)))
	}
	if requests := usage.Detail["requests"]; requests > 0 && usage.Detail["usage_missing"] >= requests {
		// All attempts lack usage: the accumulator's zero is not reported usage.
		return
	}
	span.SetAttributes(attribute.Int64("llm.token_count.prompt", int64(usage.InputTokens)), attribute.Int64("llm.token_count.completion", int64(usage.OutputTokens)), attribute.Int64("llm.token_count.total", int64(usage.TotalTokens)), attribute.Int64("gen_ai.usage.input_tokens", int64(usage.InputTokens)), attribute.Int64("gen_ai.usage.output_tokens", int64(usage.OutputTokens)))
	for key, value := range usage.Detail {
		switch key {
		case "cache_read":
			span.SetAttributes(attribute.Int64("gen_ai.usage.cache_read.input_tokens", int64(value)))
		case "cache_write":
			span.SetAttributes(attribute.Int64("gen_ai.usage.cache_creation.input_tokens", int64(value)))
		case "reasoning":
			span.SetAttributes(attribute.Int64("gen_ai.usage.reasoning.output_tokens", int64(value)))
		}
	}
	// Usage.Model is the configured request alias, not an observed response model.
}
