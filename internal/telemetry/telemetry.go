// Package telemetry exports the server's metrics over OTLP/HTTP: HTTP
// traffic, Go runtime health, publishes and the size of the data directory.
// There are no traces and no logs.
//
// It is off unless OTEL_EXPORTER_OTLP_ENDPOINT is set. Off means no
// exporter, no reader, no goroutine and no network, and main does not wrap
// the handler at all. Local development, the tests and self-hosted servers
// therefore run exactly as they did before.
package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// scope names the instrumentation library on every metric this server
// records itself.
const scope = "github.com/anup-a/byagent"

// defaultInterval is how often metrics are pushed when
// OTEL_METRIC_EXPORT_INTERVAL is unset: the SDK's own default, and the one
// sample a minute Grafana Cloud bills as a single series.
const defaultInterval = time.Minute

// Enabled reports whether telemetry is configured: an OTLP endpoint is set
// and the SDK has not been switched off with OTEL_SDK_DISABLED=true.
func Enabled() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED")), "true") {
		return false
	}
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""
}

// Start sets up the global MeterProvider and Go runtime metrics when
// Enabled, and does nothing otherwise. The returned shutdown flushes what
// is still buffered; call it once, last, with a bounded context. It is never
// nil.
//
// The exporter reads the standard OTEL_EXPORTER_OTLP_* variables itself
// (endpoint, headers, timeout), and the resource takes OTEL_SERVICE_NAME
// and OTEL_RESOURCE_ATTRIBUTES on top of the defaults set here.
func Start(ctx context.Context) (shutdown func(context.Context) error, err error) {
	noop := func(context.Context) error { return nil }
	if !Enabled() {
		return noop, nil
	}
	exp, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return noop, err
	}
	res, err := newResource(ctx)
	if err != nil {
		return noop, err
	}
	var opts []sdkmetric.PeriodicReaderOption
	interval := defaultInterval
	if v := os.Getenv("OTEL_METRIC_EXPORT_INTERVAL"); v != "" {
		// The SDK reads the variable itself; this copy is only for the log line.
		if ms, err := time.ParseDuration(v + "ms"); err == nil {
			interval = ms
		}
	} else {
		opts = append(opts, sdkmetric.WithInterval(defaultInterval))
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithView(sizeView),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, opts...)),
	)
	otel.SetMeterProvider(mp)
	if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
		return mp.Shutdown, err
	}
	slog.Info("telemetry on", "exporter", "otlp/http", "endpoint", redact(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
		"interval", interval.String(), "service", serviceName(res))
	return mp.Shutdown, nil
}

// newResource names this process. The defaults come first and the
// environment last, so OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES win
// over them, including service.version for builds that carry no VCS stamp.
func newResource(ctx context.Context) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{semconv.ServiceName("byagent")}
	if v := version(); v != "" {
		attrs = append(attrs, semconv.ServiceVersion(v))
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(attrs...),
		resource.WithHost(),
		resource.WithTelemetrySDK(),
		resource.WithFromEnv(),
	)
	// A partial resource is still worth exporting; only a missing one is not.
	if res != nil && errors.Is(err, resource.ErrPartialResource) {
		slog.Warn("telemetry: partial resource", "err", err)
		return res, nil
	}
	return res, err
}

// version is the git commit the binary was built from, when the Go
// toolchain stamped one. A build from a tree without .git (the Docker image,
// whose context leaves .git out) has none; set
// OTEL_RESOURCE_ATTRIBUTES=service.version=<sha> there instead.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	rev, dirty := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev != "" && dirty {
		rev += "-dirty"
	}
	return rev
}

func serviceName(res *resource.Resource) string {
	v, _ := res.Set().Value(semconv.ServiceNameKey)
	return v.AsString()
}

// redact keeps any credentials written into the endpoint URL out of the
// log. Headers, where credentials normally go, are never logged.
func redact(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "(unparseable)"
	}
	return u.Redacted()
}
