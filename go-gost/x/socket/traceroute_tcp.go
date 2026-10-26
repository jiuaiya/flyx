package socket

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// Separate protocols retain their own paths: flow hashing may send ICMP
// and TCP through different routers even at the same TTL.
func tracerouteAuto(host string, maxHops, family, port int, fallback bool) (TracerouteResponse, error) {
	dst, err := resolveTraceAddr(host, family)
	if err != nil {
		return TracerouteResponse{Host: host}, err
	}
	tcp, tcpErr := tracerouteTCP(host, dst, maxHops, port)
	if !fallback || (tcpErr == nil && completeTrace(tcp)) {
		return tcp, tcpErr
	}
	other, icmpErr := tracerouteICMP(host, dst, maxHops)
	if tcpErr != nil {
		if icmpErr == nil {
			return other, nil
		}
		return tcp, tcpErr
	}
	if icmpErr == nil {
		tcp.Fallback = &other
	}
	return tcp, nil
}

func completeTrace(r TracerouteResponse) bool {
	if !r.Reached || len(r.Hops) == 0 {
		return false
	}
	for _, h := range r.Hops {
		if h.IP == "" {
			return false
		}
	}
	return true
}

// SYN probes use a reserved local port and a random sequence base. ICMP
// quotes include only the first 8 TCP bytes, enough to match the ports and
// sequence without relying on optional payload. No connection is completed.
func tracerouteTCP(host string, dst net.IP, maxHops, port int) (TracerouteResponse, error) {
	resp := TracerouteResponse{Host: host, IP: dst.String(), Protocol: "tcp"}
	network, rawNetwork, tp := "tcp4", "ip4:tcp", traceV4
	udpNetwork := "udp4"
	if dst.To4() == nil {
		network, rawNetwork, tp = "tcp6", "ip6:tcp", traceV6
		udpNetwork = "udp6"
	}
	route, err := net.DialUDP(udpNetwork, nil, &net.UDPAddr{IP: dst, Port: port})
	if err != nil {
		return resp, fmt.Errorf("节点没有到目标的路由: %v", err)
	}
	src := route.LocalAddr().(*net.UDPAddr).IP
	_ = route.Close()
	reserved, err := net.ListenTCP(network, &net.TCPAddr{IP: src})
	if err != nil {
		return resp, fmt.Errorf("保留探测端口失败: %v", err)
	}
	defer reserved.Close()
	sourcePort := reserved.Addr().(*net.TCPAddr).Port
	conn, err := net.ListenPacket(rawNetwork, src.String())
	if err != nil {
		return resp, fmt.Errorf("需要 root 或 NET_RAW 权限: %v", err)
	}
	defer conn.Close()
	errors, err := icmp.ListenPacket(tp.network, src.String())
	if err != nil {
		return resp, fmt.Errorf("需要 root 或 NET_RAW 权限: %v", err)
	}
	defer errors.Close()

	setTTL := ipv4.NewPacketConn(conn).SetTTL
	if dst.To4() == nil {
		setTTL = ipv6.NewPacketConn(conn).SetHopLimit
	}
	base := rand.Uint32()
	var mu sync.Mutex
	sent := make(map[int]time.Time)
	hops := make(map[int]TracerouteHop)
	reachedAt := 0
	record := func(seq int, peer net.Addr, reached bool) {
		mu.Lock()
		defer mu.Unlock()
		at, ok := sent[seq]
		if !ok {
			return
		}
		ttl := (seq-1)%tracerouteMaxHops + 1
		// Only a reply from the destination can end a trace.
		reached = reached && net.ParseIP(peerIP(peer)).Equal(dst)
		if _, ok := hops[ttl]; !ok || reached {
			hops[ttl] = TracerouteHop{TTL: ttl, IP: peerIP(peer), RttMs: float64(time.Since(at).Microseconds()) / 1000}
		}
		if reached && (reachedAt == 0 || ttl < reachedAt) {
			reachedAt = ttl
		}
	}
	// All readers are stopped and joined on every return, including sends
	// that fail partway through a trace.
	var readers sync.WaitGroup
	deadline := time.Now().Add(5 * time.Second)
	_ = conn.SetReadDeadline(deadline)
	_ = errors.SetReadDeadline(deadline)
	readers.Add(2)
	go func() {
		defer readers.Done()
		buf := make([]byte, 1500)
		for {
			n, peer, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			if seq, ok := matchTCPReply(buf[:n], sourcePort, port, base); ok {
				record(seq, peer, true)
			}
		}
	}()
	go func() {
		defer readers.Done()
		buf := make([]byte, 1500)
		for {
			n, peer, err := errors.ReadFrom(buf)
			if err != nil {
				return
			}
			if seq, ok := matchTCPError(tp, buf[:n], src, dst, sourcePort, port, base); ok {
				record(seq, peer, false)
			}
		}
	}()
	defer func() {
		_ = conn.SetReadDeadline(time.Now())
		_ = errors.SetReadDeadline(time.Now())
		readers.Wait()
	}()
	for round := 0; round < tracerouteRounds; round++ {
		for ttl := 1; ttl <= maxHops; ttl++ {
			mu.Lock()
			_, seen := hops[ttl]
			stop := reachedAt != 0 && ttl > reachedAt
			mu.Unlock()
			if seen || stop {
				continue
			}
			if err := setTTL(ttl); err != nil {
				return resp, fmt.Errorf("设置探测 TTL 失败: %v", err)
			}
			seq := round*tracerouteMaxHops + ttl
			packet := traceSYN(src, dst, sourcePort, port, base+uint32(seq))
			mu.Lock()
			sent[seq] = time.Now()
			mu.Unlock()
			if _, err := conn.WriteTo(packet, &net.IPAddr{IP: dst}); err != nil {
				return resp, fmt.Errorf("发送 TCP 探测包失败: %v", err)
			}
			time.Sleep(5 * time.Millisecond)
		}
		for end := time.Now().Add(2 * time.Second); time.Now().Before(end); {
			mu.Lock()
			complete := reachedAt != 0
			for ttl := 1; complete && ttl <= reachedAt; ttl++ {
				_, complete = hops[ttl]
			}
			mu.Unlock()
			if complete {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	last := reachedAt
	resp.Reached = last != 0
	if last == 0 {
		for ttl := range hops {
			if ttl > last {
				last = ttl
			}
		}
	}
	resp.Hops = make([]TracerouteHop, 0, last)
	for ttl := 1; ttl <= last; ttl++ {
		h := hops[ttl]
		h.TTL = ttl
		resp.Hops = append(resp.Hops, h)
	}
	return resp, nil
}

func traceSYN(src, dst net.IP, sourcePort, port int, seq uint32) []byte {
	packet := make([]byte, 20)
	binary.BigEndian.PutUint16(packet[0:2], uint16(sourcePort))
	binary.BigEndian.PutUint16(packet[2:4], uint16(port))
	binary.BigEndian.PutUint32(packet[4:8], seq)
	packet[12], packet[13] = 5<<4, 0x02 // 20-byte header, SYN
	binary.BigEndian.PutUint16(packet[14:16], 65535)
	var pseudo []byte
	if dst.To4() != nil {
		pseudo = make([]byte, 12)
		copy(pseudo[:4], src.To4())
		copy(pseudo[4:8], dst.To4())
		pseudo[9] = 6
		binary.BigEndian.PutUint16(pseudo[10:12], uint16(len(packet)))
	} else {
		pseudo = make([]byte, 40)
		copy(pseudo[:16], src.To16())
		copy(pseudo[16:32], dst.To16())
		binary.BigEndian.PutUint32(pseudo[32:36], uint32(len(packet)))
		pseudo[39] = 6
	}
	var sum uint32
	for _, data := range [][]byte{pseudo, packet} {
		for i := 0; i+1 < len(data); i += 2 {
			sum += uint32(binary.BigEndian.Uint16(data[i : i+2]))
		}
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	binary.BigEndian.PutUint16(packet[16:18], ^uint16(sum))
	return packet
}

func matchTCPReply(b []byte, sourcePort, port int, base uint32) (int, bool) {
	if len(b) < 20 || int(b[12]>>4)*4 < 20 || int(b[12]>>4)*4 > len(b) ||
		int(binary.BigEndian.Uint16(b[:2])) != port || int(binary.BigEndian.Uint16(b[2:4])) != sourcePort ||
		b[13]&0x10 == 0 || b[13]&0x06 == 0 { // ACK plus SYN or RST
		return 0, false
	}
	seq := binary.BigEndian.Uint32(b[8:12]) - base - 1
	return int(seq), seq >= 1 && seq <= tracerouteRounds*tracerouteMaxHops
}

func matchTCPError(tp traceProto, b []byte, src, dst net.IP, sourcePort, port int, base uint32) (int, bool) {
	m, err := icmp.ParseMessage(tp.proto, b)
	if err != nil {
		return 0, false
	}
	var quote []byte
	switch body := m.Body.(type) {
	case *icmp.TimeExceeded:
		quote = body.Data
	case *icmp.DstUnreach:
		quote = body.Data
	default:
		return 0, false
	}
	var tcp []byte
	if tp.proto == tracerouteICMPProtoV6 {
		if len(quote) < 48 || quote[0]>>4 != 6 || quote[6] != 6 ||
			!net.IP(quote[8:24]).Equal(src) || !net.IP(quote[24:40]).Equal(dst) {
			return 0, false
		}
		tcp = quote[40:]
	} else {
		if len(quote) < 28 || quote[0]>>4 != 4 || quote[9] != 6 ||
			!net.IP(quote[12:16]).Equal(src) || !net.IP(quote[16:20]).Equal(dst) {
			return 0, false
		}
		ihl := int(quote[0]&0x0f) * 4
		if ihl < 20 || len(quote) < ihl+8 {
			return 0, false
		}
		tcp = quote[ihl:]
	}
	if int(binary.BigEndian.Uint16(tcp[:2])) != sourcePort || int(binary.BigEndian.Uint16(tcp[2:4])) != port {
		return 0, false
	}
	seq := binary.BigEndian.Uint32(tcp[4:8]) - base
	return int(seq), seq >= 1 && seq <= tracerouteRounds*tracerouteMaxHops
}
