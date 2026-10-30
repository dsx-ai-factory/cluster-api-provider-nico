// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSetupWithoutEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	t.Cleanup(func() { otel.SetTextMapPropagator(previousPropagator) })
	shutdown, err := Setup(context.Background())
	require.NoError(t, err)
	assert.Same(t, previousProvider, otel.GetTracerProvider())
	require.NoError(t, shutdown(context.Background()))
}

func TestSetupWithPartialResource(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "k8s.pod.name=controller-pod,missing-value")
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	previousHandler := otel.GetErrorHandler()
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
		otel.SetErrorHandler(previousHandler)
	})
	res, err := serviceResource(context.Background())
	require.ErrorIs(t, err, resource.ErrPartialResource)
	pod, ok := res.Set().Value("k8s.pod.name")
	require.True(t, ok)
	assert.Equal(t, "controller-pod", pod.AsString())
	var diagnostic string
	logger := funcr.New(func(_, entry string) { diagnostic = entry }, funcr.Options{})
	LogError(logger, err, "resource detection failed")
	assert.Contains(t, diagnostic, "partial resource")
	assert.NotContains(t, diagnostic, "missing-value")
	shutdown, err := Setup(context.Background())
	require.NoError(t, err)
	assert.NotSame(t, previousProvider, otel.GetTracerProvider())
	require.NoError(t, shutdown(context.Background()))
}

func TestServiceResource(t *testing.T) {
	for _, serviceName := range []string{"", "custom-controller"} {
		t.Run(serviceName, func(t *testing.T) {
			t.Setenv("OTEL_SERVICE_NAME", serviceName)
			t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "k8s.pod.name=controller-pod")
			res, err := serviceResource(context.Background())
			require.NoError(t, err)
			name, ok := res.Set().Value("service.name")
			require.True(t, ok)
			wantName := serviceName
			if wantName == "" {
				wantName = "capnico-controller"
			}
			assert.Equal(t, wantName, name.AsString())
			pod, ok := res.Set().Value("k8s.pod.name")
			require.True(t, ok)
			assert.Equal(t, "controller-pod", pod.AsString())
			for _, key := range []string{"telemetry.sdk.name", "telemetry.sdk.language", "telemetry.sdk.version"} {
				value, exists := res.Set().Value(attribute.Key(key))
				assert.True(t, exists)
				assert.NotEmpty(t, value.AsString())
			}
		})
	}
}

func TestSDKErrorHandler(t *testing.T) {
	var entries []string
	logger := funcr.New(func(_, entry string) { entries = append(entries, entry) }, funcr.Options{})
	handler := newErrorHandler(logger)
	err := fmt.Errorf("server detail: %w", status.Error(grpccodes.Unavailable, "collector detail"))
	handler.Handle(err)
	handler.Handle(err)
	require.Len(t, entries, 1)
	assert.Contains(t, entries[0], "OpenTelemetry SDK error")
	assert.Contains(t, entries[0], "Unavailable")
	assert.Contains(t, entries[0], "*status.Error")
	assert.NotContains(t, entries[0], "server detail")
	assert.NotContains(t, entries[0], "collector detail")
}

func TestHTTPClientPropagation(t *testing.T) {
	exporter := captureSpans(t)
	ctx, parent := otel.Tracer("test").Start(context.Background(), "reconcile")
	defer parent.End()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.invalid/instance", nil)
	require.NoError(t, err)
	base := &testTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
		current := trace.SpanContextFromContext(req.Context())
		remoteCtx := otel.GetTextMapPropagator().Extract(context.Background(), propagation.HeaderCarrier(req.Header))
		remote := trace.SpanContextFromContext(remoteCtx)
		assert.Equal(t, current.TraceID(), remote.TraceID())
		assert.Equal(t, current.SpanID(), remote.SpanID())
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("response"))}, nil
	}}
	original := &http.Client{Transport: base}
	traced := HTTPClient(original)
	assert.Same(t, base, original.Transport)
	response, err := traced.Do(request)
	require.NoError(t, err)
	assert.Empty(t, request.Header.Get("traceparent"))
	assert.Empty(t, exporter.GetSpans())
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, parent.SpanContext().TraceID(), spans[0].SpanContext.TraceID())
	assert.Equal(t, parent.SpanContext().SpanID(), spans[0].Parent.SpanID())
	assert.Equal(t, trace.SpanKindClient, spans[0].SpanKind)
}

func TestWaitingStageIsNotAnError(t *testing.T) {
	exporter := captureSpans(t)
	ctx, end := StartReconcile(context.Background(), "NicoMachine", "test", "machine")
	ctx, stage := StartStage(ctx, "capnico.check_readiness")
	MarkWaiting(ctx, "instance_not_ready")
	EndStage(stage, "waiting", nil)
	end(nil)
	spans := exporter.GetSpans()
	require.Len(t, spans, 2)
	assert.Equal(t, codes.Unset, spans[0].Status.Code)
	attributes := make(map[string]any)
	for _, value := range spans[0].Attributes {
		attributes[string(value.Key)] = value.Value.AsInterface()
	}
	assert.Equal(t, "waiting", attributes["capnico.outcome"])
	assert.Equal(t, "instance_not_ready", attributes["capnico.wait_reason"])
	assert.Equal(t, spans[1].SpanContext.SpanID(), spans[0].Parent.SpanID())
}

func captureSpans(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(resource.Empty()),
	)
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
		require.NoError(t, provider.Shutdown(context.Background()))
	})
	return exporter
}

type testTransport struct {
	roundTrip func(*http.Request) (*http.Response, error)
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.roundTrip(req)
}
