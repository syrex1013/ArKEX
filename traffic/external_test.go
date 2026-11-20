package traffic

import (
	"strings"
	"testing"
	"time"
)

func TestRecordExternalStoresLikeProxyTraffic(t *testing.T) {
	tr, _ := openTraffic(t)

	big := strings.Repeat("A", 2<<20) // spills to the blob store
	err := tr.RecordExternal(ExternalExchange{
		Method:          "POST",
		URL:             "https://api.example.com/v1/login",
		Status:          200,
		RequestHeaders:  map[string]string{"content-type": "application/json", "authorization": "Bearer xyz"},
		RequestBody:     `{"user":"u1","password":"p1"}`,
		ResponseHeaders: map[string]string{"content-type": "application/json"},
		ResponseBody:    `{"token":"tok"}`,
	})
	if err != nil {
		t.Fatalf("RecordExternal small: %v", err)
	}
	if err := tr.RecordExternal(ExternalExchange{
		Method:          "GET",
		URL:             "https://api.example.com/v1/items?page=1",
		Status:          404,
		ResponseHeaders: map[string]string{"content-type": "text/html"},
		ResponseBody:    big,
	}); err != nil {
		t.Fatalf("RecordExternal spilled: %v", err)
	}

	if n, _ := tr.Count(); n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	rows, total, err := tr.Page(PageQuery{Query: "login", RespMin: -1, RespMax: -1}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("filtered page = %d rows (total %d), want 1", len(rows), total)
	}
	req, resp, err := tr.Get(rows[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req, "POST /v1/login") || !strings.Contains(req, "Bearer xyz") || !strings.Contains(req, `"user":"u1"`) {
		t.Fatalf("request side incomplete: %q", req)
	}
	if !strings.Contains(resp, `{"token":"tok"}`) {
		t.Fatalf("response side incomplete: %q", resp)
	}

	// The spilled entry must be FTS-searchable by its body content too.
	rows, total, err = tr.Page(PageQuery{Body: strings.Repeat("A", 64), RespMin: -1, RespMax: -1}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("spilled-body FTS search = %d rows (total %d), want 1", len(rows), total)
	}
}

// TestEgressStamp verifies the via/egress provenance columns: ingested rows
// carry via=external with no egress stamp (the server never made that
// connection), the probe result is stamped onto stored rows, and Page
// round-trips both.
func TestEgressStamp(t *testing.T) {
	tr, _ := openTraffic(t)
	probeCalls := 0
	tr.EgressProbe = func() string { probeCalls++; return "tunneled" }

	if err := tr.RecordExternal(ExternalExchange{
		Method:       "GET",
		URL:          "https://ext.example.com/",
		Status:       200,
		ResponseBody: "ok",
	}); err != nil {
		t.Fatal(err)
	}

	// Storage path shared with proxy recording: stamp a proxy exchange through
	// insertExchange directly (record() needs a live mproxy Flow).
	err := tr.insertExchange("9999-0001", time.Now().Unix(), "target.example.com", "GET", "/", "https://target.example.com/", 200, "text/html", 0, 2, "GET / HTTP/1.1\n", storedBody{inline: []byte("ok")}, "HTTP 200\n", storedBody{inline: []byte("ok")}, "proxy", tr.egressStamp())
	if err != nil {
		t.Fatal(err)
	}
	if probeCalls != 1 {
		t.Fatalf("probe calls = %d, want 1", probeCalls)
	}

	rows, total, err := tr.Page(PageQuery{RespMin: -1, RespMax: -1}, 0, 10)
	if err != nil || total != 2 {
		t.Fatalf("page = %d rows (err %v), want 2", total, err)
	}
	byID := map[string]ExchangeMeta{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	if r := byID["9999-0001"]; r.Via != "proxy" || r.Egress != "tunneled" {
		t.Fatalf("proxy row via=%q egress=%q, want proxy/tunneled", r.Via, r.Egress)
	}
	for _, r := range rows {
		if r.ID == "9999-0001" {
			continue
		}
		if r.Via != "external" || r.Egress != "" {
			t.Fatalf("external row via=%q egress=%q, want external/empty", r.Via, r.Egress)
		}
	}

	// No probe installed: the stamp is empty (unknown), capture still works.
	tr2, _ := openTraffic(t)
	if s := tr2.egressStamp(); s != "" {
		t.Fatalf("stamp without probe = %q, want empty", s)
	}
}

func TestRecordExternalRejectsBadInput(t *testing.T) {
	tr, _ := openTraffic(t)
	cases := []ExternalExchange{
		{Method: "GET", URL: "not a url"},
		{Method: "GET", URL: ""},
		{Method: "", URL: "https://api.example.com/"},
		{Method: "GET\r\nX-Injected: 1", URL: "https://api.example.com/"},
		{Method: "GET", URL: "https://api.example.com/", RequestHeaders: map[string]string{"X-Bad\nName": "1"}},
		{Method: "GET", URL: "https://api.example.com/", RequestHeaders: map[string]string{"x-ok": "one\r\ntwo"}},
		{Method: "GET", URL: "https://api.example.com/", ResponseHeaders: map[string]string{"x-capture": "ok\r\nX-Forged-Header: injected"}},
		{Method: "GET", URL: "https://api.example.com/", ResponseHeaders: map[string]string{"x-nul": "a\x00b"}},
	}
	for i, c := range cases {
		if err := tr.RecordExternal(c); err == nil {
			t.Fatalf("case %d: expected an error for %+v", i, c)
		}
	}
	if n, _ := tr.Count(); n != 0 {
		t.Fatalf("rejected exchanges were stored: count=%d", n)
	}
}
