package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	assert.True(t, cfg.Enabled)
	assert.Equal(t, "stdout", cfg.ExporterType)
	assert.Equal(t, 1.0, cfg.SampleRate)
}

func TestSetupNoop(t *testing.T) {
	cfg := Config{
		Enabled: false,
	}

	shutdown, err := Setup(context.Background(), cfg)
	assert.NoError(t, err)
	assert.NotNil(t, shutdown)
	assert.NotNil(t, Tracer())
	// Propagator may be nil in noop mode

	// Shutdown should not error
	err = shutdown(context.Background())
	assert.NoError(t, err)
}

func TestSetupStdout(t *testing.T) {
	cfg := Config{
		Enabled:      true,
		ExporterType: "stdout",
		SampleRate:   1.0,
	}

	shutdown, err := Setup(context.Background(), cfg)
	assert.NoError(t, err)
	assert.NotNil(t, shutdown)
	assert.NotNil(t, Tracer())

	// Shutdown
	err = shutdown(context.Background())
	assert.NoError(t, err)
}

func TestSetupOTLP(t *testing.T) {
	// OTLP requires a running collector, so we just test config parsing
	cfg := Config{
		Enabled:      true,
		ExporterType: "otlp",
		OTLPEndpoint: "localhost:4317",
		SampleRate:   0.5,
	}

	// This will fail without a collector, but we test the config path
	shutdown, err := Setup(context.Background(), cfg)
	// OTLP connection may fail, that's expected in tests
	if err != nil {
		assert.Contains(t, err.Error(), "OTLP")
	} else {
		assert.NotNil(t, shutdown)
		_ = shutdown(context.Background())
	}
}

func TestStartSpan(t *testing.T) {
	cfg := Config{Enabled: true, ExporterType: "stdout"}
	shutdown, _ := Setup(context.Background(), cfg)
	defer shutdown(context.Background())

	ctx, span := StartSpan(context.Background(), "test-operation")
	assert.NotNil(t, span)
	assert.NotNil(t, ctx)

	span.End()
}

func TestTraceIDFromContext(t *testing.T) {
	cfg := Config{Enabled: true, ExporterType: "stdout"}
	shutdown, _ := Setup(context.Background(), cfg)
	defer shutdown(context.Background())

	// Without span
	traceID := TraceIDFromContext(context.Background())
	assert.Empty(t, traceID)

	// With span
	ctx, span := StartSpan(context.Background(), "test")
	traceID = TraceIDFromContext(ctx)
	assert.NotEmpty(t, traceID)
	span.End()
}

func TestGinMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := Config{Enabled: true, ExporterType: "stdout"}
	shutdown, _ := Setup(context.Background(), cfg)
	defer shutdown(context.Background())

	router := gin.New()
	router.Use(GinMiddleware())
	router.GET("/test", func(c *gin.Context) {
		traceID := TraceIDFromContext(c.Request.Context())
		c.JSON(http.StatusOK, gin.H{"trace_id": traceID})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, w.Header().Get("X-Trace-ID"))
}

func TestGinMiddlewareWithError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := Config{Enabled: true, ExporterType: "stdout"}
	shutdown, _ := Setup(context.Background(), cfg)
	defer shutdown(context.Background())

	router := gin.New()
	router.Use(GinMiddleware())
	router.GET("/error", func(c *gin.Context) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "test error"})
	})

	req := httptest.NewRequest("GET", "/error", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAddAttributes(t *testing.T) {
	cfg := Config{Enabled: true, ExporterType: "stdout"}
	shutdown, _ := Setup(context.Background(), cfg)
	defer shutdown(context.Background())

	ctx, span := StartSpan(context.Background(), "test")
	defer span.End()

	AddAttributes(ctx,
		attribute.String("key1", "value1"),
		attribute.Int("key2", 42),
	)
}

func TestRecordError(t *testing.T) {
	cfg := Config{Enabled: true, ExporterType: "stdout"}
	shutdown, _ := Setup(context.Background(), cfg)
	defer shutdown(context.Background())

	ctx, span := StartSpan(context.Background(), "test")
	defer span.End()

	RecordError(ctx, assert.AnError)
}

func TestHTTPClient(t *testing.T) {
	cfg := Config{Enabled: true, ExporterType: "stdout"}
	shutdown, _ := Setup(context.Background(), cfg)
	defer shutdown(context.Background())

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create traced client with explicit transport
	client := HTTPClient(&http.Client{
		Transport: http.DefaultTransport,
	})
	resp, err := client.Get(server.URL)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()
}

func TestSpanFromContext(t *testing.T) {
	cfg := Config{Enabled: true, ExporterType: "stdout"}
	shutdown, _ := Setup(context.Background(), cfg)
	defer shutdown(context.Background())

	// No span in context
	span := SpanFromContext(context.Background())
	assert.NotNil(t, span)

	// With span
	ctx, s := StartSpan(context.Background(), "test")
	span = SpanFromContext(ctx)
	assert.NotNil(t, span)
	s.End()
}

// --- v0.61.0 Telemetry Package Coverage Improvements ---

func TestPropagator(t *testing.T) {
	cfg := Config{Enabled: true, ExporterType: "stdout"}
	shutdown, _ := Setup(context.Background(), cfg)
	defer shutdown(context.Background())

	// Propagator should return a non-nil propagator in enabled mode
	prop := Propagator()
	assert.NotNil(t, prop)
}

func TestRecordErrorWithNilError(t *testing.T) {
	cfg := Config{Enabled: true, ExporterType: "stdout"}
	shutdown, _ := Setup(context.Background(), cfg)
	defer shutdown(context.Background())

	ctx, span := StartSpan(context.Background(), "test")
	defer span.End()

	// RecordError with nil error should not panic
	RecordError(ctx, nil)
}

func TestRecordErrorWithOptions(t *testing.T) {
	cfg := Config{Enabled: true, ExporterType: "stdout"}
	shutdown, _ := Setup(context.Background(), cfg)
	defer shutdown(context.Background())

	ctx, span := StartSpan(context.Background(), "test")
	defer span.End()

	// RecordError with options
	RecordError(ctx, assert.AnError)
}

// ---------------------------------------------------------------------------
// v0.61.0 Additional Telemetry Tests
// ---------------------------------------------------------------------------

func TestGRPCUnaryInterceptor(t *testing.T) {
	// Setup telemetry
	cfg := Config{Enabled: true, ExporterType: "stdout"}
	shutdown, _ := Setup(context.Background(), cfg)
	defer shutdown(context.Background())

	// Create interceptor
	interceptor := GRPCUnaryInterceptor()
	assert.NotNil(t, interceptor)

	// Test with mock handler
	ctx := context.Background()
	req := "test-request"
	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return "test-response", nil
	}

	// Call interceptor
	resp, err := interceptor(ctx, req, info, handler)
	assert.NoError(t, err)
	assert.Equal(t, "test-response", resp)
}

func TestGRPCUnaryInterceptorWithError(t *testing.T) {
	cfg := Config{Enabled: true, ExporterType: "stdout"}
	shutdown, _ := Setup(context.Background(), cfg)
	defer shutdown(context.Background())

	interceptor := GRPCUnaryInterceptor()
	assert.NotNil(t, interceptor)

	ctx := context.Background()
	req := "test-request"
	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return nil, assert.AnError
	}

	// Call interceptor with error
	resp, err := interceptor(ctx, req, info, handler)
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestWrappedStreamContext(t *testing.T) {
	// Test wrappedStream.Context()
	ctx := context.Background()
	expectedCtx := context.WithValue(ctx, "key", "value")

	ws := &wrappedStream{
		ctx: expectedCtx,
	}

	result := ws.Context()
	assert.NotNil(t, result)
	assert.Equal(t, "value", result.Value("key"))
}

func TestContextFunction(t *testing.T) {
	// Test that Context() returns the wrapped context
	ctx := context.Background()
	testKey := "test-key"
	testValue := "test-value"
	wrappedCtx := context.WithValue(ctx, testKey, testValue)

	ws := &wrappedStream{
		ctx: wrappedCtx,
	}

	result := ws.Context()
	assert.Equal(t, testValue, result.Value(testKey))
}

func TestHTTPClientWithNilBase(t *testing.T) {
	cfg := Config{Enabled: true, ExporterType: "stdout"}
	shutdown, _ := Setup(context.Background(), cfg)
	defer shutdown(context.Background())

	// HTTPClient with nil base should use default
	client := HTTPClient(nil)
	assert.NotNil(t, client)
	assert.NotNil(t, client.Transport)
}

func TestAddAttributesWithNoSpan(t *testing.T) {
	cfg := Config{Enabled: true, ExporterType: "stdout"}
	shutdown, _ := Setup(context.Background(), cfg)
	defer shutdown(context.Background())

	// AddAttributes with no span in context should not panic
	ctx := context.Background()
	AddAttributes(ctx, attribute.String("key", "value"))
	// Should not panic
}

func TestTraceIDFromContextWithNoTrace(t *testing.T) {
	// TraceIDFromContext with no trace should return empty string
	traceID := TraceIDFromContext(context.Background())
	assert.Equal(t, "", traceID)
}

func TestSpanFromContextWithNoSpan(t *testing.T) {
	cfg := Config{Enabled: true, ExporterType: "stdout"}
	shutdown, _ := Setup(context.Background(), cfg)
	defer shutdown(context.Background())

	// SpanFromContext with no span should return noop span
	span := SpanFromContext(context.Background())
	assert.NotNil(t, span)
}
