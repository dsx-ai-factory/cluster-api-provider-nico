// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestHTTPClientResponseSpan(t *testing.T) {
	readErr := errors.New("read failed")
	closeErr := errors.New("close failed")
	for _, tc := range []struct {
		name       string
		readErr    error
		closeErr   error
		earlyClose bool
		wantStatus codes.Code
	}{
		{name: "EOF", wantStatus: codes.Unset},
		{name: "early close", earlyClose: true, wantStatus: codes.Unset},
		{name: "read failure", readErr: readErr, wantStatus: codes.Error},
		{name: "close failure", earlyClose: true, closeErr: closeErr, wantStatus: codes.Error},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exporter := captureSpans(t)
			ctx, parent := otel.Tracer("test").Start(context.Background(), "reconcile")
			defer parent.End()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.invalid/instance", nil)
			require.NoError(t, err)
			var reader io.Reader = strings.NewReader("response")
			if tc.readErr != nil {
				reader = iotest.ErrReader(tc.readErr)
			}
			base := &testTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
				current := trace.SpanContextFromContext(req.Context())
				remoteCtx := otel.GetTextMapPropagator().Extract(context.Background(), propagation.HeaderCarrier(req.Header))
				remote := trace.SpanContextFromContext(remoteCtx)
				assert.Equal(t, current.TraceID(), remote.TraceID())
				assert.Equal(t, current.SpanID(), remote.SpanID())
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       &testBody{Reader: reader, closeErr: tc.closeErr},
				}, nil
			}}
			original := &http.Client{Transport: base}
			traced := HTTPClient(original)
			assert.Same(t, base, original.Transport)
			traced.CloseIdleConnections()
			assert.True(t, base.closedIdle)
			response, err := traced.Transport.RoundTrip(request)
			require.NoError(t, err)
			assert.Empty(t, request.Header.Get("traceparent"))
			assert.Empty(t, exporter.GetSpans())
			if tc.earlyClose {
				_, err = response.Body.Read(make([]byte, 1))
				require.NoError(t, err)
				assert.Empty(t, exporter.GetSpans())
			} else {
				_, err = io.ReadAll(response.Body)
				assert.ErrorIs(t, err, tc.readErr)
				require.Len(t, exporter.GetSpans(), 1)
			}
			assert.ErrorIs(t, response.Body.Close(), tc.closeErr)
			spans := exporter.GetSpans()
			require.Len(t, spans, 1)
			assert.Equal(t, parent.SpanContext().TraceID(), spans[0].SpanContext.TraceID())
			assert.Equal(t, parent.SpanContext().SpanID(), spans[0].Parent.SpanID())
			assert.Equal(t, tc.wantStatus, spans[0].Status.Code)
		})
	}
}

func TestHTTPClientEndsWithoutResponseBody(t *testing.T) {
	transportErr := errors.New("request failed")
	for _, tc := range []struct {
		name       string
		response   *http.Response
		err        error
		wantStatus codes.Code
	}{
		{name: "transport failure", err: transportErr, wantStatus: codes.Error},
		{name: "empty response", response: &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, wantStatus: codes.Unset},
		{name: "HTTP failure", response: &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody}, wantStatus: codes.Error},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exporter := captureSpans(t)
			request, err := http.NewRequest(http.MethodGet, "http://example.invalid/instance", nil)
			require.NoError(t, err)
			traced := HTTPClient(&http.Client{Transport: &testTransport{roundTrip: func(*http.Request) (*http.Response, error) {
				return tc.response, tc.err
			}}})
			_, err = traced.Transport.RoundTrip(request)
			assert.ErrorIs(t, err, tc.err)
			spans := exporter.GetSpans()
			require.Len(t, spans, 1)
			assert.Equal(t, tc.wantStatus, spans[0].Status.Code)
		})
	}
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
	roundTrip  func(*http.Request) (*http.Response, error)
	closedIdle bool
}

func (t *testTransport) CloseIdleConnections() { t.closedIdle = true }

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.roundTrip(req)
}

type testBody struct {
	io.Reader
	closeErr error
}

func (b *testBody) Close() error { return b.closeErr }
