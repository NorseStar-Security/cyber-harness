// Package otel exports the existing AOP stream as OTel traces, logs and metrics.
package otel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/eventbus"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/protobuf/proto"
)

type Options struct {
	// Endpoint is the OTLP/HTTP base URL, e.g. http://127.0.0.1:24318.
	Endpoint string
	// Signal endpoints are complete URLs; they override Endpoint without path appending.
	TraceEndpoint, LogEndpoint, MetricEndpoint string
	// ProviderName identifies the configured client/provider dialect, not the
	// vendor behind a compatible gateway. Unknown is explicit, never model-derived.
	ProviderName string
	ServiceName  string
	Project      string
	// CaptureContent includes bounded input/output and event payloads. Off by default.
	CaptureContent bool
	Queue          int
	// Exporter optionally supplies an application-owned destination. The extension
	// owns its lifecycle once loaded; omit to use the OTLP/HTTP exporter.
	Exporter sdktrace.SpanExporter
	// Endpoints or supplied exporters enable their signals independently.
	// The extension owns every exporter.
	LogExporter                 sdklog.Exporter
	MetricExporter              sdkmetric.Exporter
	DisableLogs, DisableMetrics bool
	// MetricInterval defaults to 10 seconds; Flush/Close also export metrics.
	MetricInterval time.Duration
	// ResourceAttributes apply equally to all signals, e.g. service.instance.id.
	ResourceAttributes []attribute.KeyValue
}

type Extension struct {
	options        Options
	lifecycle      sync.Mutex
	sub            *eventbus.Subscription[*aop.Event]
	provider       *sdktrace.TracerProvider
	exporter       *checkedExporter
	logs           *sdklog.LoggerProvider
	metrics        *sdkmetric.MeterProvider
	failures       exportErrors
	signals        *signals
	reportedDrops  uint64
	bridge         *aopBridge
	closeDone      chan struct{}
	closeErr       error
	loaded, closed bool
}

var _ extension.Extension = (*Extension)(nil)

func New(options Options) (*Extension, error) {
	if err := options.resolveEndpoints(); err != nil {
		return nil, err
	}
	if options.ProviderName == "" {
		options.ProviderName = "unknown"
	}
	if options.ServiceName == "" {
		options.ServiceName = "cyber"
	}
	if options.Project == "" {
		options.Project = options.ServiceName
	}
	if options.Queue <= 0 {
		options.Queue = 512
	}
	if options.MetricInterval <= 0 {
		options.MetricInterval = 10 * time.Second
	}
	options.ResourceAttributes = append([]attribute.KeyValue(nil), options.ResourceAttributes...)
	return &Extension{options: options}, nil
}

func (e *Extension) Load(scope *extension.Scope) error {
	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()
	if e.closed || e.closeDone != nil {
		return fmt.Errorf("OTel extension is closing or closed")
	}
	if e.loaded {
		return nil
	}
	stream, err := extension.Use[*events.Stream](scope)
	if err != nil {
		return err
	}
	exporter := e.options.Exporter
	if exporter == nil && e.options.TraceEndpoint != "" {
		exporter, err = otlptracehttp.New(scope.Init(), otlptracehttp.WithEndpointURL(e.options.TraceEndpoint), otlptracehttp.WithTimeout(5*time.Second))
		if err != nil {
			return err
		}
	}
	attrs := append([]attribute.KeyValue{
		attribute.String("service.name", e.options.ServiceName),
		attribute.String("service.instance.id", aop.EnvelopeID()),
		attribute.String("openinference.project.name", e.options.Project),
		attribute.String("cyber.otel.gen_ai.version", genAIVersion),
	}, e.options.ResourceAttributes...)
	res := resource.NewSchemaless(attrs...)
	// Set owns cleanup, including Close after a partially successful Load.
	if exporter != nil {
		e.exporter = &checkedExporter{SpanExporter: exporter}
		e.provider = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(e.exporter, sdktrace.WithMaxQueueSize(2048), sdktrace.WithMaxExportBatchSize(512), sdktrace.WithBatchTimeout(100*time.Millisecond), sdktrace.WithExportTimeout(5*time.Second), sdktrace.WithBlocking()),
		)
	}
	if err := e.loadSignals(scope.Init(), res); err != nil {
		return err
	}
	tracer := noop.NewTracerProvider().Tracer(instrumentationName)
	if e.provider != nil {
		tracer = e.provider.Tracer(instrumentationName, trace.WithInstrumentationVersion(instrumentationVersion))
	}
	e.bridge = newAOPBridge(signalTracer{Tracer: tracer, signals: e.signals}, e.options.CaptureContent)
	e.bridge.signals = e.signals
	e.bridge.provider = e.options.ProviderName
	e.sub, err = stream.Consume(eventbus.SubscribeOptions[*aop.Event]{
		Buffer: e.options.Queue, MaxBytes: 16 << 20,
		Filter: func(event *aop.Event) bool {
			if event == nil {
				return false
			}
			switch event.Payload.(type) {
			case *aop.Event_MessageDelta, *aop.Event_ToolCallDelta, *aop.Event_ProviderFrame:
				return false
			}
			return true
		},
		Size:  func(event *aop.Event) int64 { return int64(proto.Size(event)) },
		Clone: func(event *aop.Event) *aop.Event { return proto.CloneOf(event) },
	}, e.bridge.consume)
	if err != nil {
		return err
	}
	e.loaded = true
	return nil
}

func (e *Extension) Flush(ctx context.Context) error {
	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()
	if e.sub == nil || e.closed {
		return nil
	}
	if e.closeDone != nil {
		return fmt.Errorf("%w: OTel extension is closing", extension.ErrCloseIncomplete)
	}
	if err := e.sub.Flush(ctx); err != nil {
		return err
	}
	e.recordDrops()
	var traceErr, logErr, metricErr error
	if e.provider != nil {
		traceErr = e.provider.ForceFlush(ctx)
	}
	if e.exporter != nil {
		traceErr = errors.Join(traceErr, e.exporter.Err())
	}
	if e.logs != nil {
		logErr = e.logs.ForceFlush(ctx)
	}
	if e.metrics != nil {
		metricErr = e.metrics.ForceFlush(ctx)
	}
	return errors.Join(e.subscriptionError(), traceErr, logErr, metricErr, e.failures.Err())
}

func (e *Extension) Close(ctx context.Context) error {
	e.lifecycle.Lock()
	if e.closeDone == nil {
		if err := ctx.Err(); err != nil {
			e.lifecycle.Unlock()
			return errors.Join(extension.ErrCloseIncomplete, err)
		}
		e.closeDone = make(chan struct{})
		// SDK Shutdown is one-shot. Let the extension own its completion instead
		// of passing a caller deadline that cannot be retried inside the SDK.
		go e.finishClose(context.WithoutCancel(ctx))
	}
	done := e.closeDone
	e.lifecycle.Unlock()
	select {
	case <-done:
		return e.closeErr
	default:
	}
	select {
	case <-done:
		return e.closeErr
	case <-ctx.Done():
		return errors.Join(extension.ErrCloseIncomplete, ctx.Err())
	}
}

func (e *Extension) finishClose(ctx context.Context) {
	var errs []error
	if e.sub != nil {
		errs = append(errs, e.sub.Close(ctx))
	}
	if e.bridge != nil {
		e.bridge.finishOpen()
	}
	e.recordDrops()
	errs = append(errs, e.shutdown(ctx), e.subscriptionError(), e.failures.Err())
	if e.exporter != nil {
		errs = append(errs, e.exporter.Err())
	}
	e.lifecycle.Lock()
	e.closeErr = errors.Join(errs...)
	if errors.Is(e.closeErr, context.Canceled) || errors.Is(e.closeErr, context.DeadlineExceeded) {
		// Export timeouts are retained diagnostics after shutdown has completed.
		// They must not tell Set.Close that this extension still owns live work.
		e.closeErr = completedCloseError{e.closeErr}
	}
	e.closed = true
	close(e.closeDone)
	e.lifecycle.Unlock()
}

// No Unwrap: caller cancellation means incomplete cleanup, while an exporter
// timeout after completed shutdown does not. Preserve other error identities
// and types without exposing those two lifecycle cancellation sentinels.
type completedCloseError struct{ err error }

func (e completedCloseError) Error() string {
	return "OTel shutdown completed with signal errors: " + e.err.Error()
}
func (e completedCloseError) Is(target error) bool {
	return target != context.Canceled && target != context.DeadlineExceeded && errors.Is(e.err, target)
}
func (e completedCloseError) As(target any) bool { return errors.As(e.err, target) }

func (e *Extension) subscriptionError() error {
	if e.sub == nil {
		return nil
	}
	var overflow error
	if dropped := e.sub.Dropped(); dropped > 0 {
		overflow = fmt.Errorf("OTel signals incomplete: %d AOP events dropped", dropped)
	}
	return errors.Join(e.sub.Err(), overflow)
}

// Batch exporters report asynchronous failures through the SDK error handler.
// Retain them as well so Flush/Close cannot claim a successful export after loss.
type checkedExporter struct {
	sdktrace.SpanExporter
	mu  sync.Mutex
	err error
}

func (e *checkedExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	err := e.SpanExporter.ExportSpans(ctx, spans)
	if err != nil {
		e.mu.Lock()
		if e.err == nil {
			e.err = fmt.Errorf("export OTel spans: %w", err)
		}
		e.mu.Unlock()
	}
	return err
}

func (e *checkedExporter) Err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err
}

func clip(value string) string {
	const limit = 4096
	if len(value) <= limit {
		return value
	}
	return strings.ToValidUTF8(value[:limit], "") + "…"
}
