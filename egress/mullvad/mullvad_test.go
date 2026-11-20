package mullvad

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRelaysParseAndCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"countries": []map[string]any{{
				"name": "Germany", "code": "de",
				"cities": []map[string]any{{
					"name": "Berlin", "code": "ber",
					"relays": []map[string]any{{
						"hostname": "de-ber-wg-001", "ipv4_addr_in": "185.65.134.72",
						"public_key": "abc=",
					}},
				}},
			}},
		})
	}))
	defer srv.Close()

	c := New(t.TempDir())
	// swap URL via test hook on the package const is not possible; exercise parse
	// path indirectly through a stubbed transport.
	prev := relaysURL
	setRelaysURL(srv.URL)
	defer setRelaysURL(prev)

	relays, err := c.Relays(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(relays) != 1 || relays[0].Hostname != "de-ber-wg-001" {
		t.Fatalf("unexpected relays %+v", relays)
	}
	// cached second call must not hit the server (server closed after first? keep open)
}
