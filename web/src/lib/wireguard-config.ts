const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;

export function parseWireGuardConfig(text: string) {
  const sections = new Set<string>();
  const fields = new Map<string, string>();
  let section = "";
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.split(/[#;]/, 1)[0].trim();
    if (!line) continue;
    const header = /^\[(interface|peer)\]$/i.exec(line);
    if (header) {
      section = header[1].toLowerCase();
      if (sections.has(section)) return null;
      sections.add(section);
      continue;
    }
    const entry = /^([a-z][a-z0-9]*)\s*=\s*(.*)$/i.exec(line);
    if (!section || !entry) return null;
    const name = `${section}.${entry[1].toLowerCase()}`;
    if (name === "peer.presharedkey" && entry[2].trim()) return null;
    if (name !== "interface.privatekey" && name !== "interface.address" && name !== "peer.endpoint") continue;
    if (fields.has(name)) return null;
    fields.set(name, entry[2].trim());
  }

  const privateKey = fields.get("interface.privatekey");
  const addresses = (fields.get("interface.address") ?? "").split(",").map((value) => value.trim());
  const ipv4Addresses = addresses.filter((value) => IPV4.test(value.split("/")[0]));
  if (ipv4Addresses.length !== 1) return null;
  const [address, prefix, ...extra] = ipv4Addresses[0].split("/");
  if (extra.length || (prefix !== undefined && !/^(?:[0-9]|[12][0-9]|3[0-2])$/.test(prefix))) return null;

  const endpoint = /^([a-z0-9.-]+):(\d{1,5})$/i.exec(fields.get("peer.endpoint") ?? "");
  if (!privateKey || !/^[A-Za-z0-9+/]{43}=$/.test(privateKey) || !endpoint) return null;
  if (Number(endpoint[2]) < 1 || Number(endpoint[2]) > 65535) return null;
  return { privateKey, address, endpointHost: endpoint[1].toLowerCase().replace(/\.$/, "") };
}

export function findWireGuardRelay(endpointHost: string, servers: { hostname: string; ipv4?: string }[]) {
  const hostname = endpointHost.replace(/\.mullvad\.net$/, "");
  const matches = servers.filter(
    (server) => server.hostname.toLowerCase() === hostname || server.ipv4 === endpointHost,
  );
  return matches.length === 1 ? matches[0].hostname : undefined;
}
