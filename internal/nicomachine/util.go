// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nicomachine

func KeyedSlicesMatch[ActualValue any, ExpectedValue any, Key comparable](
	actualValues []ActualValue,
	expectedValues []ExpectedValue,
	actualKey func(ActualValue) Key,
	expectedKey func(ExpectedValue) Key,
) bool {
	return SlicesMatchFunc(actualValues, expectedValues, func(actual ActualValue, expected ExpectedValue) bool {
		return actualKey(actual) == expectedKey(expected)
	})
}

// SlicesMatchFunc reports whether every expected value can be paired with its
// own distinct actual value satisfying matches, ignoring order. Lengths must
// be equal.
//
// Pairing is greedy: the first unused actual that matches an expected is taken
// and never reconsidered. That is complete when matches is an equivalence
// (KeyedSlicesMatch is that case). It is not complete for an arbitrary
// predicate — if two expected values share overlapping matches and one is
// strictly more specific, this can return false even though a pairing exists.
func SlicesMatchFunc[ActualValue any, ExpectedValue any](
	actualValues []ActualValue,
	expectedValues []ExpectedValue,
	matches func(ActualValue, ExpectedValue) bool,
) bool {
	if len(actualValues) != len(expectedValues) {
		return false
	}

	matched := make([]bool, len(actualValues))
	for _, expected := range expectedValues {
		found := false
		for i, actual := range actualValues {
			if matched[i] || !matches(actual, expected) {
				continue
			}
			matched[i] = true
			found = true
			break
		}
		if !found {
			return false
		}
	}

	return true
}
