package handler

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Narrow backbone prefixes remain useful when the ASN service is unavailable.
// Access networks are kept separate from international premium backbones.
var routeBackbones = []struct{ prefix, net string }{
	{"59.43.0.0/16", "CN2"}, {"202.97.0.0/16", "163"},
	{"219.158.0.0/16", "4837"}, {"218.105.0.0/16", "9929"},
	{"210.51.0.0/16", "9929"}, {"210.14.0.0/16", "9929"}, {"210.78.0.0/16", "9929"},
	{"223.118.0.0/15", "CMI"}, {"223.120.0.0/15", "CMI"},
	{"221.183.0.0/16", "9808"}, {"111.24.0.0/16", "9808"},
	{"203.22.178.0/23", "CTGNet"}, {"203.22.182.0/24", "CTGNet"},
	{"203.128.224.0/24", "CTGNet"},
	{"2408:8120::/32", "9929"}, {"2402:4f00:f000::/48", "CMIN2"},
	{"2402:4f00::/32", "CMI"}, {"2400:9380::/32", "CTGNet"},
	{"2409:8080::/32", "9808"}, {"240e::/20", "CT"},
	{"2401:8a00::/32", "10099"},
}

var routeASNs = map[string]string{
	"4809": "CN2", "4134": "CT", "23764": "CTGNet",
	"9929": "9929", "4837": "4837", "10099": "10099",
	"58453": "CMI", "58807": "CMIN2", "9808": "9808",
	"4538": "CERNET", "23910": "CERNET2", "23911": "CERNET2", "7497": "CSTNET",
}

func canonicalHopIP(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || a.Zone() != "" {
		return ""
	}
	return a.Unmap().String()
}

func publicHopIP(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	return a.IsGlobalUnicast() && !a.IsPrivate() && !netip.MustParsePrefix("100.64.0.0/10").Contains(a)
}

func routeHopNet(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	for _, b := range routeBackbones {
		if netip.MustParsePrefix(b.prefix).Contains(a.Unmap()) {
			return b.net
		}
	}
	return ""
}

func hopNet(ip string) string {
	if !publicHopIP(ip) {
		return ""
	}
	return refineHopNet(ip, routeHopNet(ip))
}

// Prefer ASN attribution when available, including IPv4 addresses outside
// the short prefix table. A domestic mobile hop alone is not proof of CMI.
func refineHopNet(ip, prefixNet string) string {
	if asn := hopOriginASN(ip); asn != "" {
		if n := routeASNs[asn]; n != "" {
			// Preserve the backbone-only 202.97 label rather than generic CT.
			if n == "CT" && prefixNet == "163" {
				return "163"
			}
			return n
		}
		return ""
	}
	return prefixNet
}

func asnQueryName(ip string) string {
	addr := net.ParseIP(ip)
	if addr == nil {
		return ""
	}
	if v4 := addr.To4(); v4 != nil {
		return fmt.Sprintf("%d.%d.%d.%d.origin.asn.cymru.com", v4[3], v4[2], v4[1], v4[0])
	}
	var b strings.Builder
	for i := len(addr) - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "%x.%x.", addr[i]&0x0f, addr[i]>>4)
	}
	return b.String() + "origin6.asn.cymru.com"
}

type hopASNEntry struct {
	asn   string
	until time.Time
}

var (
	hopASNMu     sync.Mutex
	hopASNCache  = make(map[string]hopASNEntry)
	hopASNFlight = make(map[string]chan struct{})
	hopASNSem    = make(chan struct{}, 16)
)

var hopASNLookup = func(name string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return net.DefaultResolver.LookupTXT(ctx, name)
}

func cachedHopASN(ip string) string {
	hopASNMu.Lock()
	defer hopASNMu.Unlock()
	return hopASNCache[canonicalHopIP(ip)].asn
}

func hopOriginASN(ip string) string {
	ip = canonicalHopIP(ip)
	if !publicHopIP(ip) {
		return ""
	}
	hopASNMu.Lock()
	if e, ok := hopASNCache[ip]; ok && time.Now().Before(e.until) {
		hopASNMu.Unlock()
		return e.asn
	}
	if done, ok := hopASNFlight[ip]; ok {
		hopASNMu.Unlock()
		<-done
		return cachedHopASN(ip)
	}
	done := make(chan struct{})
	hopASNFlight[ip] = done
	hopASNMu.Unlock()

	hopASNSem <- struct{}{}
	txt, err := hopASNLookup(asnQueryName(ip))
	<-hopASNSem
	asn := ""
	if err == nil {
		// A multi-origin prefix can list several ASNs or TXT records. Prefer
		// a recognized backbone over an unrelated first origin.
		for _, record := range txt {
			for _, origin := range strings.Fields(strings.SplitN(record, "|", 2)[0]) {
				if strings.Trim(origin, "0123456789") != "" {
					continue
				}
				if asn == "" || routeASNs[origin] != "" {
					asn = origin
				}
				if routeASNs[asn] != "" {
					break
				}
			}
			if routeASNs[asn] != "" {
				break
			}
		}
	}
	ttl := 24 * time.Hour
	if asn == "" {
		ttl = time.Minute
	}
	hopASNMu.Lock()
	// Bound memory on panels managing many nodes; in-flight lookups remain
	// independent of the cache and can safely repopulate it.
	if len(hopASNCache) >= 8192 {
		hopASNCache = make(map[string]hopASNEntry)
	}
	hopASNCache[ip] = hopASNEntry{asn: asn, until: time.Now().Add(ttl)}
	delete(hopASNFlight, ip)
	close(done)
	hopASNMu.Unlock()
	return asn
}

// Resolve distinct public addresses in bounded parallel batches before
// classifying them. Failures are briefly cached so rendering the same trace
// does not synchronously repeat every failed DNS request.
func warmHopASNs(rawHops []interface{}) {
	seen := make(map[string]bool)
	var wg sync.WaitGroup
	for _, raw := range rawHops {
		m, _ := raw.(map[string]interface{})
		ip := canonicalHopIP(asString(m["ip"]))
		if !publicHopIP(ip) || seen[ip] {
			continue
		}
		seen[ip] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			hopOriginASN(ip)
		}()
	}
	wg.Wait()
}
