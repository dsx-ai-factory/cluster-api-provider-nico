// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package fake

import (
	"log"
	"net/http"
	"time"
)

// LogRequests writes one line per request, so the local development loop shows
// the traffic the provider is actually generating. Without it an idle fake and
// a fake nobody is calling look identical.
//
// It is applied by the fake binary rather than by Handler, so tests exercising
// the same routes stay quiet.
func LogRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()

		next.ServeHTTP(recorder, r)

		log.Printf("%s %s -> %d (%s)",
			r.Method,
			r.URL.RequestURI(),
			recorder.status,
			time.Since(start).Round(time.Millisecond),
		)
	})
}

// statusRecorder captures the status code so it can be logged after the
// handler has run.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
