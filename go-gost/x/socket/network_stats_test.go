package socket

import (
	"testing"

	psnet "github.com/shirou/gopsutil/v3/net"
)

func TestReportedNetworkTotalsExcludeContainerCopies(t *testing.T) {
	counters := []psnet.IOCountersStat{
		{Name: "eth0", BytesRecv: 1000, BytesSent: 2000},
		{Name: "ens3", BytesRecv: 3000, BytesSent: 4000},
	}
	for _, name := range []string{"lo", "docker0", "veth1234", "br-1234", "cni0", "flannel.1", "cali123", "kube-ipvs0"} {
		counters = append(counters, psnet.IOCountersStat{Name: name, BytesRecv: 90000, BytesSent: 90000})
	}
	stats := reportedNetworkTotals(counters)
	if stats.BytesReceived != 4000 || stats.BytesTransmitted != 6000 {
		t.Fatalf("container interfaces counted: %+v", stats)
	}
}
