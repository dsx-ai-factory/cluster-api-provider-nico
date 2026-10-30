// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"
	"google.golang.org/grpc/status"
	ctrl "sigs.k8s.io/controller-runtime"
)

const instrumentationName = "github.com/dsx-ai-factory/cluster-api-provider-nico"

// Setup enables OTLP/gRPC export when an exporter endpoint is configured.
func Setup(ctx context.Context) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}
	otel.SetErrorHandler(newErrorHandler(ctrl.Log.WithName("tracing")))
	res, err := serviceResource(ctx)
	if err != nil {
		if !errors.Is(err, resource.ErrPartialResource) {
			return nil, err
		}
		ctrl.Log.WithName("tracing").Info("incomplete tracing resource; continuing with available attributes",
			"reason", resource.ErrPartialResource.Error())
	}
	exporter, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

func serviceResource(ctx context.Context) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", "capnico-controller")),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
}

func newErrorHandler(logger logr.Logger) otel.ErrorHandler {
	limiter := rate.NewLimiter(rate.Every(time.Minute), 1)
	return otel.ErrorHandlerFunc(func(err error) {
		if limiter.Allow() {
			LogError(logger, err, "OpenTelemetry SDK error")
		}
	})
}

// LogError reports typed causes without logging arbitrary configuration or server text.
func LogError(logger logr.Logger, err error, message string) {
	var errorTypes []string
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		errorTypes = append(errorTypes, fmt.Sprintf("%T", cause))
	}
	reason := "unclassified"
	for _, known := range []error{context.Canceled, context.DeadlineExceeded, syscall.ECONNREFUSED, syscall.ETIMEDOUT, resource.ErrPartialResource} {
		if errors.Is(err, known) {
			reason = known.Error()
			break
		}
	}
	logger.Error(nil, message, "errorTypes", errorTypes, "grpcCode", status.Code(err).String(), "reason", reason)
}

// StartReconcile starts a reconcile span and correlates its context logger.
func StartReconcile(ctx context.Context, kind, namespace, name string) (context.Context, func(error)) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, kind+".Reconcile", trace.WithAttributes(
		attribute.String("k8s.resource.kind", kind),
		attribute.String("k8s.namespace.name", namespace),
		attribute.String("k8s.resource.name", name),
		attribute.String("capnico.outcome", "completed"),
	))
	if sc := span.SpanContext(); sc.IsValid() {
		ctx = ctrl.LoggerInto(ctx, ctrl.LoggerFrom(ctx).WithValues("traceID", sc.TraceID().String(), "spanID", sc.SpanID().String()))
	}
	return ctx, func(err error) {
		if err != nil {
			span.SetAttributes(attribute.String("capnico.outcome", "failed"))
			span.SetStatus(codes.Error, "reconciliation failed")
		}
		span.End()
	}
}

// StartStage starts a logical operation within a reconciliation.
func StartStage(ctx context.Context, name string) (context.Context, trace.Span) {
	return otel.Tracer(instrumentationName).Start(ctx, name)
}

// EndStage records a bounded outcome without exporting raw error contents.
func EndStage(span trace.Span, outcome string, err error) {
	if err != nil {
		outcome = "failed"
		span.SetStatus(codes.Error, "stage failed")
	}
	span.SetAttributes(attribute.String("capnico.outcome", outcome))
	span.End()
}

// MarkWaiting records an expected wait without marking the span as an error.
func MarkWaiting(ctx context.Context, reason string) {
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String("capnico.outcome", "waiting"),
		attribute.String("capnico.wait_reason", reason),
	)
}

// HTTPClient copies the client and instruments NICo requests with OpenTelemetry.
func HTTPClient(client *http.Client) *http.Client {
	traced := *client
	traced.Transport = otelhttp.NewTransport(client.Transport)
	return &traced
}
