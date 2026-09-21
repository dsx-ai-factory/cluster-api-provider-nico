// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Workload cluster client", func() {
	ginkgo.It("rejects exec credential plugins", func() {
		kubeconfig, err := workloadKubeconfigWithExecProvider("https://127.0.0.1")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		workloadClient, err := newWorkloadClusterClient(kubeconfig, newScheme())
		gomega.Expect(workloadClient).To(gomega.BeNil())
		gomega.Expect(err).To(gomega.MatchError("workload cluster kubeconfig must not use an exec credential plugin"))
	})
})
