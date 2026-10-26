package socket

import (
	"encoding/binary"
	"net"
	"strings"
	"testing"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

func TestMatchTCPReplies(t *testing.T) {
	const sourcePort, port, base = 40000, 443, uint32(0xfffffff0)
	for _, flags := range []byte{0x12, 0x14} {
		b := make([]byte, 20)
		binary.BigEndian.PutUint16(b[:2], port)
		binary.BigEndian.PutUint16(b[2:4], sourcePort)
		ack := base
		ack += 32 // sequence wraps; probe 31 plus SYN's one byte
		binary.BigEndian.PutUint32(b[8:12], ack)
		b[12], b[13] = 5<<4, flags
		if seq, ok := matchTCPReply(b, sourcePort, port, base); !ok || seq != 31 {
			t.Fatalf("flags=%x seq=%d ok=%v", flags, seq, ok)
		}
		for name, mutate := range map[string]func([]byte){
			"port":   func(b []byte) { b[0]++ },
			"ack":    func(b []byte) { b[8]++ },
			"flags":  func(b []byte) { b[13] = 0x10 },
			"header": func(b []byte) { b[12] = 15 << 4 },
		} {
			bad := append([]byte(nil), b...)
			mutate(bad)
			if _, ok := matchTCPReply(bad, sourcePort, port, base); ok {
				t.Errorf("matched invalid %s", name)
			}
		}
		for n := 0; n < 20; n++ {
			if _, ok := matchTCPReply(b[:n], sourcePort, port, base); ok {
				t.Fatal("matched truncated TCP header")
			}
		}
	}
}

func TestMatchTCPQuotedProbes(t *testing.T) {
	for _, family := range []int{4, 6} {
		tp, src, dst, header := traceV4, net.ParseIP("192.0.2.1"), net.ParseIP("198.51.100.9"), 24
		if family == 6 {
			tp, src, dst, header = traceV6, net.ParseIP("2001:db8::1"), net.ParseIP("2001:db8::9"), 40
		}
		quote := make([]byte, header+8)
		if family == 4 {
			quote[0], quote[9] = 0x46, 6 // includes IPv4 options
			copy(quote[12:16], src.To4())
			copy(quote[16:20], dst.To4())
		} else {
			quote[0], quote[6] = 0x60, 6
			copy(quote[8:24], src.To16())
			copy(quote[24:40], dst.To16())
		}
		copy(quote[header:], traceSYN(src, dst, 40000, 443, 1007)[:8])
		wrap := func(q []byte, unreachable bool) []byte {
			m := icmp.Message{Type: ipv4.ICMPTypeTimeExceeded, Body: &icmp.TimeExceeded{Data: q}}
			if family == 6 {
				m.Type = ipv6.ICMPTypeTimeExceeded
			}
			if unreachable {
				m.Type, m.Body = ipv4.ICMPTypeDestinationUnreachable, &icmp.DstUnreach{Data: q}
				if family == 6 {
					m.Type = ipv6.ICMPTypeDestinationUnreachable
				}
			}
			b, err := m.Marshal(nil)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		for _, unreachable := range []bool{false, true} {
			b := wrap(quote, unreachable)
			if seq, ok := matchTCPError(tp, b, src, dst, 40000, 443, 1000); !ok || seq != 7 {
				t.Fatalf("IPv%d unreachable=%v: seq=%d ok=%v", family, unreachable, seq, ok)
			}
			if _, ok := matchTCPError(tp, b, src, dst, 40001, 443, 1000); ok {
				t.Fatal("matched another trace's source port")
			}
			if _, ok := matchTCPError(tp, b, src, dst, 40000, 80, 1000); ok {
				t.Fatal("matched another target port")
			}
			if _, ok := matchTCPError(tp, b, src, dst, 40000, 443, 2000); ok {
				t.Fatal("matched another trace's sequence")
			}
		}
		for n := 0; n < len(quote); n++ {
			if _, ok := matchTCPError(tp, wrap(quote[:n], false), src, dst, 40000, 443, 1000); ok {
				t.Fatal("matched truncated quote")
			}
		}
		quote[0] = 0
		if _, ok := matchTCPError(tp, wrap(quote, false), src, dst, 40000, 443, 1000); ok {
			t.Fatal("matched malformed IP header")
		}
	}
}

func TestTracerouteTCPLoopback(t *testing.T) {
	for _, addr := range []string{"127.0.0.1", "::1"} {
		t.Run(addr, func(t *testing.T) {
			network := "tcp4"
			if addr == "::1" {
				network = "tcp6"
			}
			listener, err := net.ListenTCP(network, &net.TCPAddr{IP: net.ParseIP(addr)})
			if err != nil {
				t.Skipf("loopback unavailable: %v", err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			defer listener.Close()
			resp, err := tracerouteTCP(addr, net.ParseIP(addr), 3, port)
			if err != nil && strings.Contains(err.Error(), "NET_RAW") {
				t.Skip("no raw socket permission")
			}
			if err != nil {
				t.Fatal(err)
			}
			if !resp.Reached || len(resp.Hops) != 1 || resp.Hops[0].IP != addr || resp.Protocol != "tcp" {
				t.Fatalf("open port trace: %+v", resp)
			}
			// A closed port's RST+ACK also proves arrival.
			listener.Close()
			resp, err = tracerouteTCP(addr, net.ParseIP(addr), 3, port)
			if err != nil || !resp.Reached || len(resp.Hops) != 1 {
				t.Fatalf("closed port trace: %+v, %v", resp, err)
			}
		})
	}
}

func TestTracerouteRequestValidation(t *testing.T) {
	w := &WebSocketReporter{}
	for _, request := range []TracerouteRequest{
		{Host: "127.0.0.1", Protocol: "udp"},
		{Host: "127.0.0.1", Port: -1},
		{Host: "127.0.0.1", Port: 65536},
		{Host: "127.0.0.1", Family: 5},
	} {
		if _, err := w.handleTraceroute(request); err == nil {
			t.Errorf("accepted invalid request: %+v", request)
		}
	}
}

func TestICMPUnreachableDoesNotMeanReached(t *testing.T) {
	for _, tp := range []traceProto{traceV4, traceV6} {
		dst := net.ParseIP("198.51.100.1")
		var exceeded []byte
		unreachable := icmp.Type(ipv4.ICMPTypeDestinationUnreachable)
		if tp.proto == tracerouteICMPProtoV6 {
			dst = net.ParseIP("2001:db8::1")
			exceeded = timeExceeded6(t, 42, 7, dst)
			unreachable = ipv6.ICMPTypeDestinationUnreachable
		} else {
			exceeded = timeExceeded(t, 42, 7, dst)
		}
		m, _ := icmp.ParseMessage(tp.proto, exceeded)
		b, _ := (&icmp.Message{Type: unreachable, Body: &icmp.DstUnreach{Data: m.Body.(*icmp.TimeExceeded).Data}}).Marshal(nil)
		if seq, reached, ok := matchTraceReply(tp, b, 42, dst); !ok || reached || seq != 7 {
			t.Fatalf("filtering router ended trace: seq=%d reached=%v ok=%v", seq, reached, ok)
		}
	}
}
