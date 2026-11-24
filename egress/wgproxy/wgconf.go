package wgproxy

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"strings"
)

// ParseConfig parses a standard WireGuard INI configuration (the format
// `wg-quick` accepts) into a netstack proxy Config. Exactly one [Interface]
// and one [Peer] section are required; a PresharedKey is rejected (the
// netstack client does not implement it). AllowedIPs and persistent
// keep-alives are ignored: the netstack routes all traffic into the tunnel.
func ParseConfig(text string) (Config, error) {
	var cfg Config
	section := ""
	seen := map[string]string{}
	peers := 0
	for raw := range strings.SplitSeq(text, "\n") {
		line := strings.TrimSpace(raw)
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Trim(line, "[]"))
			switch section {
			case "interface", "peer":
			default:
				return cfg, fmt.Errorf("unknown section [%s]", section)
			}
			if section == "peer" {
				peers++
				if peers > 1 {
					return cfg, fmt.Errorf("multi-peer configs are not supported (found %d peers)", peers)
				}
			}
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			return cfg, fmt.Errorf("malformed line %q", line)
		}
		key := section + "." + strings.ToLower(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		if key == "peer.presharedkey" && value != "" {
			return cfg, fmt.Errorf("peer.presharedkey is not supported")
		}
		if _, dup := seen[key]; dup {
			return cfg, fmt.Errorf("duplicate %s", key)
		}
		seen[key] = value
	}

	var err error
	if cfg.PrivateKey, err = parseKey(seen, "interface.privatekey", "private key"); err != nil {
		return cfg, err
	}
	if cfg.PeerKey, err = parseKey(seen, "peer.publickey", "peer public key"); err != nil {
		return cfg, err
	}
	cfg.Endpoint = seen["peer.endpoint"]
	if cfg.Endpoint == "" {
		return cfg, fmt.Errorf("peer.endpoint is required")
	}

	addr := firstIPV4(seen["interface.address"])
	if addr == "" {
		return cfg, fmt.Errorf("interface.address with one IPv4 address is required")
	}
	cfg.LocalAddr = addr

	cfg.DNS = firstIPV4(seen["interface.dns"]) // optional; default MullvadDNS
	return cfg, nil
}

// parseKey validates a required base64-encoded 32-byte WireGuard key field.
func parseKey(seen map[string]string, key, label string) (string, error) {
	v := seen[key]
	if v == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	raw, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(raw) != 32 {
		return "", fmt.Errorf("%s must be a 44-char base64 32-byte key", label)
	}
	return v, nil
}

// firstIPV4 returns the first plain IPv4 address (prefix stripped) from a
// comma-separated list, or "".
func firstIPV4(list string) string {
	for _, item := range strings.Split(list, ",") {
		p, err := netip.ParseAddr(strings.TrimSpace(strings.SplitN(strings.TrimSpace(item), "/", 2)[0]))
		if err == nil && p.Is4() {
			return p.String()
		}
	}
	return ""
}
