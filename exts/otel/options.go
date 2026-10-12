package otel

import (
	"fmt"
	"net/url"
	"strings"
)

// OptionsFromEnv resolves OTLP/HTTP configuration using the specification's
// signal-specific precedence. Complete signal URLs retain their exact paths;
// only a global base URL receives /v1/{signal}. No endpoints means opt-out.
// Headers, certificates and SDK resource/sampling settings remain SDK-owned.
func OptionsFromEnv(lookup func(string) (string, bool)) (Options, error) {
	value := func(key string) string {
		text, _ := lookup(key)
		return strings.TrimSpace(text)
	}
	o := Options{
		Endpoint:       value("OTEL_EXPORTER_OTLP_ENDPOINT"),
		TraceEndpoint:  value("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"),
		LogEndpoint:    value("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"),
		MetricEndpoint: value("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"),
		ServiceName:    value("OTEL_SERVICE_NAME"),
		Project:        value("CYBER_OTEL_PROJECT"),
		CaptureContent: value("CYBER_OTEL_CAPTURE_CONTENT") == "true",
	}
	if o.Endpoint == "" && o.TraceEndpoint == "" && o.LogEndpoint == "" && o.MetricEndpoint == "" {
		return o, nil
	}
	for _, signal := range []struct{ name, endpoint string }{{"TRACES", o.TraceEndpoint}, {"LOGS", o.LogEndpoint}, {"METRICS", o.MetricEndpoint}} {
		if signal.endpoint == "" && o.Endpoint == "" {
			continue
		}
		protocol := value("OTEL_EXPORTER_OTLP_" + signal.name + "_PROTOCOL")
		if protocol == "" {
			protocol = value("OTEL_EXPORTER_OTLP_PROTOCOL")
		}
		if protocol != "" && protocol != "http/protobuf" {
			return Options{}, fmt.Errorf("OTel %s requires http/protobuf, got %q", strings.ToLower(signal.name), protocol)
		}
	}
	return o, nil
}

func (o *Options) resolveEndpoints() error {
	for _, endpoint := range []string{o.Endpoint, o.TraceEndpoint, o.LogEndpoint, o.MetricEndpoint} {
		if endpoint == "" {
			continue
		}
		u, err := url.Parse(endpoint)
		if err != nil || u == nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("OTel requires an OTLP/HTTP endpoint URL")
		}
	}
	for _, signal := range []struct {
		name     string
		endpoint *string
	}{{"traces", &o.TraceEndpoint}, {"logs", &o.LogEndpoint}, {"metrics", &o.MetricEndpoint}} {
		if *signal.endpoint == "" && o.Endpoint != "" {
			// The base URL is already validated; signal URLs keep their paths.
			*signal.endpoint, _ = url.JoinPath(o.Endpoint, "v1", signal.name)
		}
	}
	if o.Exporter == nil && o.TraceEndpoint == "" && (o.DisableLogs || (o.LogExporter == nil && o.LogEndpoint == "")) && (o.DisableMetrics || (o.MetricExporter == nil && o.MetricEndpoint == "")) {
		return fmt.Errorf("OTel requires at least one signal destination")
	}
	return nil
}
