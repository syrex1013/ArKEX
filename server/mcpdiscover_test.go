package server

import "testing"

func TestStdioEnvWithProxy(t *testing.T) {
	t.Run("http proxy is injected with the CA", func(t *testing.T) {
		env := stdioEnvWithProxy(map[string]string{"MEMORY_FILE_PATH": "/app/data/mcp/memory.json"}, "http://127.0.0.1:8788", "/ca/mitmproxy-ca-cert.pem")
		for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
			if env[k] != "http://127.0.0.1:8788" {
				t.Fatalf("%s = %q, want the proxy URL", k, env[k])
			}
		}
		if env["NODE_USE_ENV_PROXY"] != "1" {
			t.Fatalf("NODE_USE_ENV_PROXY = %q, want 1", env["NODE_USE_ENV_PROXY"])
		}
		if env["NODE_EXTRA_CA_CERTS"] != "/ca/mitmproxy-ca-cert.pem" || env["SSL_CERT_FILE"] != "/ca/mitmproxy-ca-cert.pem" {
			t.Fatalf("CA vars = %q / %q, want the CA path", env["NODE_EXTRA_CA_CERTS"], env["SSL_CERT_FILE"])
		}
		if env["MEMORY_FILE_PATH"] != "/app/data/mcp/memory.json" {
			t.Fatalf("row env key lost: %q", env["MEMORY_FILE_PATH"])
		}
	})

	t.Run("socks5 proxy is left out: node crashes on it", func(t *testing.T) {
		in := map[string]string{"MEMORY_FILE_PATH": "/app/data/mcp/memory.json"}
		for _, url := range []string{"socks5://host.docker.internal:1080", "socks5h://host.docker.internal:1080"} {
			env := stdioEnvWithProxy(in, url, "/ca/mitmproxy-ca-cert.pem")
			if len(env) != 1 || env["MEMORY_FILE_PATH"] != "/app/data/mcp/memory.json" {
				t.Fatalf("%s: env mutated: %v", url, env)
			}
		}
	})

	t.Run("empty proxy leaves the env untouched", func(t *testing.T) {
		in := map[string]string{"FOO": "bar"}
		env := stdioEnvWithProxy(in, "", "/ca/mitmproxy-ca-cert.pem")
		if len(env) != 1 || env["FOO"] != "bar" {
			t.Fatalf("env mutated: %v", env)
		}
	})
}
