package nicomachine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab-master.nvidia.com/nke/cluster-api-provider-nico/internal/nicomachine"
)

func TestUtil_KeyedSlicesMatch(t *testing.T) {
	type actualValue struct {
		ID string
	}
	type expectedValue struct {
		ID string
	}
	type parameters struct {
		actual   []actualValue
		expected []expectedValue
		want     bool
	}

	tests := map[string]parameters{
		"matches values regardless of order": {
			actual: []actualValue{
				{ID: "a"},
				{ID: "b"},
			},
			expected: []expectedValue{
				{ID: "b"},
				{ID: "a"},
			},
			want: true,
		},
		"requires matching multiplicity": {
			actual: []actualValue{
				{ID: "a"},
				{ID: "b"},
			},
			expected: []expectedValue{
				{ID: "a"},
				{ID: "a"},
			},
			want: false,
		},
		"rejects different lengths": {
			actual: []actualValue{
				{ID: "a"},
			},
			expected: []expectedValue{
				{ID: "a"},
				{ID: "b"},
			},
			want: false,
		},
	}

	actualKey := func(value actualValue) string { return value.ID }
	expectedKey := func(value expectedValue) string { return value.ID }

	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			got := nicomachine.KeyedSlicesMatch(params.actual, params.expected, actualKey, expectedKey)
			assert.Equal(t, params.want, got)
		})
	}
}
