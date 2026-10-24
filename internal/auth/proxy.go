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
	// groupsHeader and adminGroups split reading from administering, the same
	// way OIDC does. Read only when adminGroups is set: without it there is
	// nothing to compare against, and reading an attacker-settable header for
	// no reason is a habit worth not having.
	groupsHeader string
	adminGroups  []string
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

// WithAdminGroups configures the reader/administrator split.
//
// Separate from NewProxy so the existing callers and their tests keep the
// signature they had: roles are an addition, not a change to what forward
// auth already did.
func (p *Proxy) WithAdminGroups(groupsHeader string, adminGroups []string) *Proxy {
	if p == nil {
		return nil
	}
	p.groupsHeader = strings.TrimSpace(groupsHeader)
	if p.groupsHeader == "" {
		p.groupsHeader = "X-Remote-Groups"
	}
	p.adminGroups = adminGroups
	return p
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
	return Identity{
		Subject: user,
		Name:    user,
		Method:  MethodProxy,
		Role:    p.roleFor(r),
	}, true
}

// roleFor reads the asserted groups, but only when there is a rule to apply.
//
// The groups header is exactly as trustworthy as the identity header — which
// is to say, trustworthy only because the trust list already decided the peer
// may assert things. Same check, same moment, no second decision.
func (p *Proxy) roleFor(r *http.Request) Role {
	if len(p.adminGroups) == 0 {
		return RoleAdmin
	}
	var groups []string
	for _, part := range strings.Split(r.Header.Get(p.groupsHeader), ",") {
		if part = strings.TrimSpace(part); part != "" {
			groups = append(groups, part)
		}
	}
	return RoleFromGroups(p.adminGroups, groups)
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
	return p.inNets(hostOf(remoteAddr))
}

// ForwardedFor returns the address a trusted proxy says a request came from.
//
// This exists for the login throttle and the ingest rate limiter, which key on
// "who is asking" and had nothing to go on but the socket address. Behind a
// reverse proxy that address is the proxy's, the same for everybody, so one
// client failing a password put every client behind that proxy into the same
// penalty -- and the throttle refuses a correct password while it is serving
// one. Anyone who could reach the login page could keep the owner out of their
// own install by failing a password every few minutes.
//
// Two rules make believing a header here safe, and both are necessary:
//
// The trust list must be set. An empty list means "believe the identity header
// from any source", which is a decision an operator can reasonably make about
// their own network, but it is not a basis for believing a forwarded address:
// with no list there is nothing to tell a proxy from a client, so a client
// would be naming its own throttle key and the throttle would be decoration.
//
// The list is walked from the right. A proxy appends the peer it received the
// request from, so the right-most entry is the one Silt's own peer added and
// the left-most is whatever the original client sent -- which is to say, a
// value an attacker chooses. Skipping trusted addresses from the right and
// taking the first that is not one yields the address the outermost trusted
// proxy actually saw. Taking the left-most instead, which is the usual way
// this is written, would hand the key straight back to the client.
func (p *Proxy) ForwardedFor(r *http.Request) (string, bool) {
	if p == nil || len(p.nets) == 0 || r == nil {
		return "", false
	}
	if !p.inNets(hostOf(r.RemoteAddr)) {
		return "", false
	}
	var chain []string
	for _, header := range r.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(header, ",") {
			if part = strings.TrimSpace(part); part != "" {
				chain = append(chain, part)
			}
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if ip := net.ParseIP(chain[i]); ip != nil && !p.inNets(chain[i]) {
			return ip.String(), true
		}
	}
	// Every hop is a trusted proxy, or none parsed. There is no client address
	// to be had, so say so rather than returning a proxy's.
	return "", false
}

// inNets reports whether an address literal is on the trust list.
func (p *Proxy) inNets(host string) bool {
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

func hostOf(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
