// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package matchers

import (
	"fmt"

	"github.com/onsi/gomega"
	"github.com/pmezard/go-difflib/difflib"
)

type matchGolden struct {
	expected     string
	expectedPath string
	actualPath   string
}

// MatchGolden compares exact string content and reports a unified diff.
func MatchGolden(expected, expectedPath, actualPath string) gomega.OmegaMatcher {
	return &matchGolden{
		expected:     expected,
		expectedPath: expectedPath,
		actualPath:   actualPath,
	}
}

func (m *matchGolden) Match(actual any) (bool, error) {
	actualString, ok := actual.(string)
	if !ok {
		return false, fmt.Errorf("MatchGolden expects a string, got %T", actual)
	}
	return actualString == m.expected, nil
}

func (m *matchGolden) FailureMessage(actual any) string {
	actualString, _ := actual.(string)
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(m.expected),
		B:        difflib.SplitLines(actualString),
		FromFile: m.expectedPath,
		ToFile:   m.actualPath,
		Context:  1,
	})
	if err != nil {
		return fmt.Sprintf("failed to generate golden diff: %v", err)
	}
	return "golden file differs:\n\n" + diff
}

func (m *matchGolden) NegatedFailureMessage(actual any) string {
	return fmt.Sprintf("expected golden file %q not to match %q", m.actualPath, m.expectedPath)
}
