// Package telemetry provides OpenTelemetry tracing integration for LuckyHarness.
package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	serviceName    = "luckyharness"
	serviceVersion = "0.27.0"
)

var (
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
)

func init() {
	tracer = trace.NewNoopTracerProvider().Tracer(serviceName)
	propagator = propagation.NewCompositeTextMapPropagator()
}

// Config holds telemetry configuration.
type Config struct {
	Enabled      bool   `yaml:"enabled"`
	ExporterType string `yaml:"exporter_type"` // stdout, otlp
	OTLPEndpoint string `yaml:"otlp_endpoint"` // e.g., localhost:4317
	SampleRate   float64 `yaml:"sample_rate"`  // 0.0 - 1.0
}

// DefaultConfig returns default telemetry configuration.
func DefaultConfig() Config {
	return Config{
		Enabled:      true,
		ExporterType: "stdout",
		SampleRate:   1.0,
	}
}

// Tracer returns the global tracer.
func Tracer() trace.Tracer {
	return tracer
}

// Propagator returns the global text map propagator.
func Propagator() propagation.TextMapPropagator {
	return propagator
}

// Setup initializes OpenTelemetry tracing.
func Setup(ctx context.Context, cfg Config) (shutdown func(context.Context) error, err error) {
	if !cfg.Enabled {
		// No-op tracer
		tracer = trace.NewNoopTracerProvider().Tracer(serviceName)
		propagator = propagation.NewCompositeTextMapPropagator()
		return func(ctx context.Context) error { return nil }, nil
	}

	// Create exporter
	var exporter sdktrace.SpanExporter
	switch cfg.ExporterType {
	case "otlp":
		if cfg.OTLPEndpoint == "" {
			cfg.OTLPEndpoint = "localhost:4317"
		}
		exporter, err = otlptrace.New(ctx,
			otlptracegrpc.NewClient(
				otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
				otlptracegrpc.WithInsecure(),
			),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
		}
	default:
		exporter, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			return nil, fmt.Errorf("failed to create stdout exporter: %w", err)
		}
	}

	// Create resource
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create sampler
	var sampler sdktrace.Sampler
	if cfg.SampleRate >= 1.0 {
		sampler = sdktrace.AlwaysSample()
	} else if cfg.SampleRate <= 0.0 {
		sampler = sdktrace.NeverSample()
	} else {
		sampler = sdktrace.TraceIDRatioBased(cfg.SampleRate)
	}

	// Create TracerProvider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	// Set global tracer provider
	otel.SetTracerProvider(tp)

	// Set global propagator
	propagator = propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
	otel.SetTextMapPropagator(propagator)

	// Create tracer
	tracer = tp.Tracer(serviceName)

	// Return shutdown function
	shutdown = func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown TracerProvider: %w", err)
		}
		return nil
	}

	return shutdown, nil
}

// StartSpan starts a new span with the given name and options.
func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return tracer.Start(ctx, name, opts...)
}

// SpanFromContext returns the current span from context.
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// TraceIDFromContext extracts the trace ID from context.
func TraceIDFromContext(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		return span.SpanContext().TraceID().String()
	}
	return ""
}

// GinMiddleware returns a Gin middleware for tracing HTTP requests.
func GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract trace context from headers
		ctx := propagator.Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))

		// Start span
		spanName := c.Request.Method + " " + c.FullPath()
		if spanName == " " {
			spanName = c.Request.Method + " " + c.Request.URL.Path
		}

		ctx, span := tracer.Start(ctx, spanName,
			trace.WithAttributes(
				attribute.String("http.request.method", c.Request.Method),
				attribute.String("url.full", c.Request.URL.String()),
				attribute.String("http.route", c.FullPath()),
			),
		)
		defer span.End()

		// Store context in request
		c.Request = c.Request.WithContext(ctx)

		// Process request
		c.Next()

		// Set span status
		status := c.Writer.Status()
		span.SetAttributes(semconv.HTTPResponseStatusCode(status))
		if status >= 400 {
			span.SetStatus(codes.Error, http.StatusText(status))
		} else {
			span.SetStatus(codes.Ok, "")
		}

		// Add trace ID to response header
		if traceID := TraceIDFromContext(ctx); traceID != "" {
			c.Header("X-Trace-ID", traceID)
		}
	}
}

// HTTPMiddleware returns a net/http middleware for tracing requests.
func HTTPMiddleware(next http.Handler) http.Handler {
	if next == nil {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		spanName := r.Method + " " + r.URL.Path
		ctx, span := tracer.Start(ctx, spanName,
			trace.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("url.path", r.URL.Path),
			),
		)
		defer span.End()

		rr := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rr, r.WithContext(ctx))

		span.SetAttributes(semconv.HTTPResponseStatusCode(rr.statusCode))
		if rr.statusCode >= 400 {
			span.SetStatus(codes.Error, http.StatusText(rr.statusCode))
		} else {
			span.SetStatus(codes.Ok, "")
		}
		if traceID := TraceIDFromContext(ctx); traceID != "" {
			rr.Header().Set("X-Trace-ID", traceID)
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

// GRPCUnaryInterceptor returns a gRPC unary server interceptor for tracing.
func GRPCUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Extract trace context from metadata
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			headers := propagation.HeaderCarrier{}
			for k, v := range md {
				headers.Set(k, v[0])
			}
			ctx = propagator.Extract(ctx, headers)
		}

		// Start span
		ctx, span := tracer.Start(ctx, info.FullMethod,
			trace.WithAttributes(
				attribute.String("rpc.system", "grpc"),
				attribute.String("rpc.method", info.FullMethod),
			),
		)
		defer span.End()

		// Call handler
		resp, err := handler(ctx, req)

		// Set span status
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			span.RecordError(err)
		} else {
			span.SetStatus(codes.Ok, "")
		}

		// Add trace ID to outgoing metadata
		if traceID := TraceIDFromContext(ctx); traceID != "" {
			md := metadata.New(map[string]string{"x-trace-id": traceID})
			ctx = metadata.NewOutgoingContext(ctx, md)
		}

		return resp, err
	}
}

// GRPCStreamInterceptor returns a gRPC stream server interceptor for tracing.
func GRPCStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()

		// Extract trace context from metadata
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			headers := propagation.HeaderCarrier{}
			for k, v := range md {
				headers.Set(k, v[0])
			}
			ctx = propagator.Extract(ctx, headers)
		}

		// Start span
		ctx, span := tracer.Start(ctx, info.FullMethod,
			trace.WithAttributes(
				attribute.String("rpc.system", "grpc"),
				attribute.String("rpc.method", info.FullMethod),
			),
		)
		defer span.End()

		// Wrap stream with new context
		wrapped := &wrappedStream{ServerStream: ss, ctx: ctx}

		// Call handler
		err := handler(srv, wrapped)

		// Set span status
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			span.RecordError(err)
		} else {
			span.SetStatus(codes.Ok, "")
		}

		return err
	}
}

// wrappedStream wraps a grpc.ServerStream with a custom context.
type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context {
	return w.ctx
}

// HTTPClient wraps an http.Client with tracing.
func HTTPClient(base *http.Client) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	return &http.Client{
		Transport: &transport{base: base.Transport},
		Timeout:   base.Timeout,
	}
}

type transport struct {
	base http.RoundTripper
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.base == nil {
		t.base = http.DefaultTransport
	}
	ctx := req.Context()

	// Start span
	ctx, span := tracer.Start(ctx, "HTTP "+req.Method,
		trace.WithAttributes(
			attribute.String("http.request.method", req.Method),
			attribute.String("url.full", req.URL.String()),
		),
	)
	defer span.End()

	// Inject trace context into headers
	propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))

	// Send request
	resp, err := t.base.RoundTrip(req.WithContext(ctx))

	// Set span status
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
	} else {
		span.SetAttributes(semconv.HTTPResponseStatusCode(resp.StatusCode))
		if resp.StatusCode >= 400 {
			span.SetStatus(codes.Error, resp.Status)
		} else {
			span.SetStatus(codes.Ok, "")
		}
	}

	return resp, err
}

// AddAttributes adds attributes to the current span.
func AddAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.SetAttributes(attrs...)
	}
}

// RecordError records an error on the current span.
func RecordError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() && err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}
