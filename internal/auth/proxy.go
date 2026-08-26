package auth

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Proxy is forward authentication: a reverse proxy in front of Silt asserts
// who the request is from, in a header.
//
// The header is only believed from an address on the trust list. That check is
// the whole security of this mode: the header is trivially settable by anyone
// who can open a socket, so without it, "authenticated" means "reached the
// port". Silt's own documented deployment — a container on a bridge network,
// with the proxy on the same network — is exactly the case where the port is
// reachable by every other container on that network.
//
// An empty trust list means the header is believed from any source, which is
// what earlier versions did unconditionally. It stays possible, because some
// deployments genuinely have nothing else on the network, but it is opt-in and
// warned about at startup rather than being the only behaviour.
type Proxy struct {
	enabled bool
	header  string
	// nets is the trust list. Empty means "trust any source", set explicitly.
	nets []*net.IPNet
	// trustAny records that the empty list was deliberate.
	trustAny bool
}

// NewProxy parses the trusted-proxy CIDRs. A bare IP is accepted and treated
// as a single-address range, because "10.0.0.5" is what people write.
func NewProxy(enabled bool, header string, trusted []string) (*Proxy, error) {
	p := &Proxy{enabled: enabled, header: strings.TrimSpace(header)}
	if p.header == "" {
		p.header = "X-Remote-User"
	}
	for _, entry := range trusted {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(entry); err == nil {
			p.nets = append(p.nets, network)
			continue
		}
		ip := net.ParseIP(entry)
		if ip == nil {
			return nil, fmt.Errorf("SILT_TRUSTED_PROXIES entry %q is not an IP address or CIDR range", entry)
		}
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		p.nets = append(p.nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	p.trustAny = len(p.nets) == 0
	return p, nil
}

// Enabled reports whether forward auth is configured.
func (p *Proxy) Enabled() bool { return p != nil && p.enabled }

// Header is the header name, for the settings screen.
func (p *Proxy) Header() string {
	if p == nil {
		return ""
	}
	return p.header
}

// TrustsAnySource reports whether the header is believed from anywhere, which
// the caller should warn about at startup.
func (p *Proxy) TrustsAnySource() bool { return p.Enabled() && p.trustAny }

// Identify returns the asserted identity, if the request is entitled to make
// one.
func (p *Proxy) Identify(r *http.Request) (Identity, bool) {
	if !p.Enabled() {
		return Identity{}, false
	}
	if !p.trusted(r.RemoteAddr) {
		return Identity{}, false
	}
	user := strings.TrimSpace(r.Header.Get(p.header))
	if user == "" {
		return Identity{}, false
	}
	return Identity{Subject: user, Name: user, Method: MethodProxy}, true
}

// trusted reports whether the immediate peer may assert an identity.
//
// RemoteAddr and nothing else: it is the address of the socket, which cannot
// be spoofed by a header. Reading X-Forwarded-For here would be circular —
// believing a header in order to decide whether to believe a header.
func (p *Proxy) trusted(remoteAddr string) bool {
	if p.trustAny {
		return true
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, network := range p.nets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
