package wgproxy

import (
	"strings"
	"testing"
)

const validConf = `[Interface]
PrivateKey = 2KZ5uYQsomethinglongenoughtobebase64pad=
Address = 10.66.123.5/32
DNS = 10.64.0.1, 1.1.1.1

[Peer]
PublicKey = x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9=
Endpoint = 185.100.100.100:51820
AllowedIPs = 0.0.0.0/0
`

// b64Key returns a valid 32-byte base64 key.
func b64Key(t *testing.T) string {
	t.Helper()
	k, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestParseConfigValid(t *testing.T) {
	priv, peer := b64Key(t), b64Key(t)
	conf := strings.ReplaceAll(validConf, "2KZ5uYQsomethinglongenoughtobebase64pad=", priv)
	conf = strings.ReplaceAll(conf, "x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9=", peer)
	cfg, err := ParseConfig(conf)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.PrivateKey != priv || cfg.PeerKey != peer {
		t.Fatalf("keys not extracted: %q %q", cfg.PrivateKey, cfg.PeerKey)
	}
	if cfg.LocalAddr != "10.66.123.5" {
		t.Fatalf("LocalAddr = %q", cfg.LocalAddr)
	}
	if cfg.Endpoint != "185.100.100.100:51820" {
		t.Fatalf("Endpoint = %q", cfg.Endpoint)
	}
	if cfg.DNS != "10.64.0.1" {
		t.Fatalf("DNS = %q", cfg.DNS)
	}
}

func TestParseConfigRejections(t *testing.T) {
	keys := strings.NewReplacer(
		"2KZ5uYQsomethinglongenoughtobebase64pad=", b64Key(t),
		"x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9=", b64Key(t),
	).Replace(validConf)

	cases := []struct {
		name, conf, want string
	}{
		{"presharedkey", strings.Replace(keys, "AllowedIPs = 0.0.0.0/0", "PresharedKey = aaaa", 1), "presharedkey"},
		{"no peer", strings.SplitN(keys, "[Peer]", 2)[0], "peer public key"},
		{"no address", strings.Replace(keys, "Address = 10.66.123.5/32\n", "", 1), "interface.address"},
		{"no endpoint", strings.Replace(keys, "Endpoint = 185.100.100.100:51820\n", "", 1), "peer.endpoint"},
		{"two peers", keys + "\n[Peer]\nPublicKey = " + b64Key(t) + "\n", "multi-peer"},
		{"malformed line", "[Interface]\nnotakv\n", "malformed"},
		{"unknown section", keys + "\n[Table]\n", "unknown section"},
	}
	for _, c := range cases {
		if _, err := ParseConfig(c.conf); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want containing %q", c.name, err, c.want)
		}
	}
}

func TestParseConfigDNSOptional(t *testing.T) {
	conf := strings.ReplaceAll(validConf, "DNS = 10.64.0.1, 1.1.1.1\n", "")
	conf = strings.NewReplacer(
		"2KZ5uYQsomethinglongenoughtobebase64pad=", b64Key(t),
		"x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9x9=", b64Key(t),
	).Replace(conf)
	cfg, err := ParseConfig(conf)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.DNS != "" {
		t.Fatalf("DNS = %q, want empty (default applies in Start)", cfg.DNS)
	}
}
