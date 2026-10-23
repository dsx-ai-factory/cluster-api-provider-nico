// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

type apiRequestMetrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

var apiMetrics = newAPIRequestMetrics()

func init() {
	metrics.Registry.MustRegister(apiMetrics.requests, apiMetrics.duration)
}

func newAPIRequestMetrics() *apiRequestMetrics {
	return &apiRequestMetrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "capnico_api_requests_total",
			Help: "Total NICo API requests by operation and HTTP status code; none indicates no response.",
		}, []string{"operation", "status_code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "capnico_api_request_duration_seconds",
			Help:    "Duration of NICo SDK requests in seconds, including response decoding.",
			Buckets: prometheus.DefBuckets,
		}, []string{"operation", "status_code"}),
	}
}

func (m *apiRequestMetrics) observe(operation string, duration time.Duration, resp *http.Response) {
	statusCode := "none"
	if resp != nil {
		statusCode = strconv.Itoa(resp.StatusCode)
	}
	m.requests.WithLabelValues(operation, statusCode).Inc()
	m.duration.WithLabelValues(operation, statusCode).Observe(duration.Seconds())
}
