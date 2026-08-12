// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nicomachine

func KeyedSlicesMatch[ActualValue any, ExpectedValue any, Key comparable](
	actualValues []ActualValue,
	expectedValues []ExpectedValue,
	actualKey func(ActualValue) Key,
	expectedKey func(ExpectedValue) Key,
) bool {
	if len(actualValues) != len(expectedValues) {
		return false
	}

	counts := make(map[Key]int, len(actualValues))
	for _, value := range actualValues {
		counts[actualKey(value)]++
	}
	for _, value := range expectedValues {
		key := expectedKey(value)
		if counts[key] == 0 {
			return false
		}
		counts[key]--
	}

	return true
}
