import { findWireGuardRelay, parseWireGuardConfig } from "../src/lib/wireguard-config.ts";
import assert from "node:assert/strict";
import { test } from "node:test";

const privateKey = Buffer.alloc(32, 1).toString("base64");
const config = [
  "[Interface]",
  `PrivateKey = ${privateKey}`,
  "Address = 10.2.3.4/32, fc00::1/128",
  "[Peer]",
  "Endpoint = 192.0.2.5:51820 # example",
].join("\r\n");

test("imports a dual-stack config and resolves its relay by endpoint IP or hostname", () => {
  const relays = [{ hostname: "test-wg-001", ipv4: "192.0.2.5" }];
  assert.deepEqual(parseWireGuardConfig(config), { privateKey, address: "10.2.3.4", endpointHost: "192.0.2.5" });
  assert.equal(findWireGuardRelay("192.0.2.5", relays), "test-wg-001");
  assert.equal(findWireGuardRelay("test-wg-001.mullvad.net", relays), "test-wg-001");
  assert.equal(findWireGuardRelay("192.0.2.6", relays), undefined);
  assert.equal(findWireGuardRelay("192.0.2.5", [...relays, { ...relays[0], hostname: "other" }]), undefined);
});

test("rejects invalid and ambiguous files", () => {
  assert.equal(parseWireGuardConfig(config.replace(privateKey, "invalid")), null);
  assert.equal(parseWireGuardConfig(config.replace("10.2.3.4/32", "10.2.3.4/33")), null);
  assert.equal(parseWireGuardConfig(config.replace(":51820", ":65536")), null);
  assert.equal(parseWireGuardConfig(`${config}\n[Peer]\nEndpoint = 192.0.2.6:51820`), null);
  assert.equal(parseWireGuardConfig(`${config}\nPresharedKey = ${privateKey}`), null);
});
