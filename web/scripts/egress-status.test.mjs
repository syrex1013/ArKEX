import assert from "node:assert/strict";
import test from "node:test";
import { egressStatus } from "../src/lib/egress-status.ts";

const base = { active: false, interface: "", egress_ip: "192.0.2.1", egress_tunneled: false };

test("shows one route with an IP only when observed for that route", () => {
  const proxy = { ...base, global_proxy_set: true };
  const mullvad = { ...proxy, up: true, egress_tunneled: true };

  assert.deepEqual(egressStatus(base), { route: "direct", state: "off", ip: base.egress_ip });
  assert.deepEqual(egressStatus(proxy), { route: "proxy", state: "unverified" });
  assert.deepEqual(egressStatus(mullvad), { route: "mullvad", state: "working", ip: base.egress_ip });
  assert.deepEqual(egressStatus({ ...mullvad, task_egress_blocked: true }), { route: "mullvad", state: "failed" });
  assert.deepEqual(egressStatus({ ...mullvad, egress_ip: "" }), { route: "mullvad", state: "unverified" });
  assert.deepEqual(egressStatus(mullvad, true), { route: "mullvad", state: "unavailable" });
  assert.deepEqual(egressStatus(undefined), { route: "direct", state: "checking" });
});
