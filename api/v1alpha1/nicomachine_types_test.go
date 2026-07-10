package v1alpha1

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNicoMachineStatusTopologyFieldsRoundTrip(t *testing.T) {
	status := NicoMachineStatus{
		MachineID: "machine-1",
		SiteID:    "site-1",
		SiteName:  "New York / A",
		VPCID:     "vpc-1",
		VPCName:   "Tenant VPC",
	}

	b, err := json.Marshal(status)
	require.NoError(t, err)

	var got NicoMachineStatus
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, status, got)
}
