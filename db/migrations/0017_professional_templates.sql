-- Seed the professional engagement template set. Idempotent: ON CONFLICT
-- (nkey) DO NOTHING never overwrites a template the operator renamed or
-- edited. nkey is the lowercased task name (db.task_templates.taskTemplateNKey).
INSERT INTO task_templates(name, nkey, description, goal, category_id, intercept_rules)
VALUES
('External Infrastructure Penetration Test', 'external infrastructure penetration test',
 $$Perform an external infrastructure penetration test against the in-scope hosts, networks and services.

Method:
- Enumerate live hosts, open ports and services from an external vantage point; fingerprint operating systems and service versions.
- Correlate every service against known vulnerabilities and verify each candidate exploitatively before reporting it; never trust a version banner alone.
- Attack the services themselves: weak or default credentials, unauthenticated access, protocol-level flaws, exposure of management planes, TLS misconfigurations.
- Attempt to pivot from any foothold: map reachable internal ranges, credentials re-use, and outbound egress paths.
- Track every action and finding with evidence (raw traffic, command output, screenshots) suitable for the final report.

Stay strictly within scope: attack only the hosts and ranges explicitly in scope, keep credential attacks at the agreed intensity, and treat anything out of scope as forbidden even if reachable.$$,
 $$Every in-scope external host has been enumerated, candidate vulnerabilities have been verified hands-on, all confirmed vulnerabilities are reported with severity and reproduction steps, and an executive summary exists. The engagement leaves no unintended side effects on out-of-scope systems.$$, NULL, '[]'::jsonb),

('Internal Network Penetration Test', 'internal network penetration test',
 $$Perform an internal network penetration test from a foothold inside the target network.

Method:
- Map the internal topology: subnets, live hosts, running services, directory services, file shares, print servers, databases and management interfaces.
- Identify trust relationships and segmentation weaknesses; attempt lateral movement across network boundaries and VLANs.
- Harvest credentials where the engagement rules allow it (memory, files, network captures, protocol negotiation downgrades) and test re-use across systems.
- Escalate privileges on compromised hosts and document every path from the initial foothold to the most privileged position achieved.
- Probe common internal services for misconfiguration: SMB, NFS, LDAP, DNS, internal web apps, backups, monitoring agents.
- Record each finding with the exact reproduction path and impact; a finding without evidence does not count.

Obey the engagement rules of engagement at all times: no destructive actions, no disruption of production services beyond the agreed intensity, and immediate containment of any access gained unintentionally.$$,
 $$The internal network is mapped, privilege escalation and lateral movement paths are demonstrated or credibly ruled out, all confirmed findings are documented with severity and evidence, and a remediation-oriented report is produced.$$, NULL, '[]'::jsonb),

('Active Directory Attack', 'active directory attack',
 $$Perform a full Active Directory security assessment against the target domain(s).

Method:
- Enumerate the directory: domains, trusts, OUs, users, groups, computers, GPOs, ACLs, delegation, and domain functional level. Map privileged groups and their effective rights.
- Hunt misconfigurations: kerberoastable and AS-REP roastable accounts, unconstrained and misconfigured delegation, weak ACL chains (GenericAll, WriteDacl, DCSync rights), GPO abuse, LDAP signing and channel binding gaps.
- Attack authentication: password spraying within the agreed intensity, NTLM relay paths, coercion (PetitPotam-class), certificate abuse via ADCS (ESC1-ESC8 where applicable), and trust attacks where multiple domains exist.
- Attempt domain dominance: route from any obtained foothold to DCSync or equivalent domain-level privilege, and document the full attack chain.
- Test defensive controls: LAPS coverage, tiering model, credential guard, and detection of the techniques used.

Every technique is logged with the tooling used, the exact commands, timestamps and the outcome, so defenders can reproduce detection.$$,
 $$The directory is fully enumerated, all candidate misconfiguration chains are verified hands-on, the deepest achieved privilege path is documented end-to-end with evidence, and each finding includes severity, impact and remediation.$$, NULL, '[]'::jsonb),

('API Security Assessment', 'api security assessment',
 $$Perform an in-depth security assessment of the target REST, GraphQL and/or gRPC APIs.

Method:
- Recover the API surface: OpenAPI/Swagger/GraphQL introspection, proto files, JavaScript bundles, mobile clients and traffic captures. Enumerate every operation, parameter and object type.
- Probe object-level authorization (BOLA/IDOR) systematically across every object reference, and function-level authorization across every role pair.
- Attack authentication and session handling: token forgery and algorithm confusion (JWT), refresh-token rotation, OAuth/OIDC flows, and session fixation.
- Probe mass assignment by adding privileged fields, excessive data exposure in responses, injection (SQLi, NoSQL, GraphQL batching), SSRF via URL-typed parameters, and unrestricted resource consumption (rate limits, pagination, file sizes).
- For GraphQL: introspection, batching/amplification, field suggestions, depth limits. For gRPC: reflection, unknown-field handling, proto smuggling.
- Validate every finding against the API contract: a claim is confirmed only with a reproducing request/response pair.

Stay within the agreed scope and rate limits; credential attacks only against the explicitly authorized endpoints.$$,
 $$The full API surface is enumerated and documented, every authorization boundary is systematically tested, all confirmed vulnerabilities carry reproducing request/response evidence with severity, and a findings report with concrete fixes is produced.$$, NULL, '[]'::jsonb),

('Cloud Security Assessment', 'cloud security assessment',
 $$Perform a cloud security posture assessment and penetration test against the target cloud environment(s) (AWS, Azure, GCP, or Kubernetes).

Method:
- Enumerate the environment with the provided read-only credentials: identities, roles, policies, trust relationships, storage, compute, network exposure, logging and monitoring state.
- Hunt privilege escalation paths within the identity model: policy misconfigurations, role chaining, wildcard actions, trust abuse, instance metadata endpoints, and cross-account trust.
- Test the external attack surface: public buckets, exposed management endpoints, misconfigured load balancers, leaked secrets in public resources, and subdomain takeover of dangling DNS.
- For Kubernetes: RBAC analysis, exposed kubelets and dashboards, escape paths from workloads, and secrets exposure.
- Evaluate detective controls: whether the techniques used would generate alerts, and where logging gaps exist.
- Every escalation path is demonstrated with the minimal necessary actions and full command evidence; no destructive changes to the customer environment.

Respect scope: only the explicitly authorized accounts, subscriptions, projects and clusters are in scope; other tenants and accounts are never touched.$$,
 $$The cloud environment is enumerated, identity-model escalation paths are verified hands-on or credibly ruled out, external exposure is fully mapped, all findings include severity and concrete remediation, and a posture report is produced.$$, NULL, '[]'::jsonb),

('Mobile Application Penetration Test', 'mobile application penetration test',
 $$Perform a full penetration test of the target mobile application (Android and/or iOS).

Method:
- Static analysis: decompile the app, map entry points, recover hardcoded secrets, API endpoints, encryption keys, and third-party SDKs. Audit custom cryptographic use and obfuscation quality.
- Dynamic analysis: instrument the runtime, bypass root/jailbreak detection and certificate pinning where present, and capture the full network conversation with backends.
- Test local storage: shared preferences, databases, caches, keychain/keystore usage, and logs for sensitive data.
- Test the client-side attack surface: exported components and deep links (Android), URL schemes and universal links (iOS), IPC boundaries, and WebView configurations.
- Test backend authorization from the mobile context: replay and modify API traffic to find object-level authorization flaws the mobile client hides.
- Verify anti-tamper and integrity checks actually detect modification.

Report every finding with the exact build analyzed, reproduction steps and evidence captures.$$,
 $$Both static and dynamic analysis are complete, all local-storage and IPC issues are enumerated, backend authorization is tested through the mobile traffic, every finding carries reproducible evidence, and a report with remediation is produced.$$, NULL, '[]'::jsonb),

('Container and Kubernetes Escape Review', 'container and kubernetes escape review',
 $$Assess container isolation and Kubernetes configuration on the target environment, focusing on escape and privilege boundaries.

Method:
- Review workload configuration: privileged pods, host mounts, host network/PID, capabilities, seccomp/AppArmor profiles, service account tokens and automount settings.
- From inside representative workloads, test escape vectors: kernel and runtime exposure, hostPath access, container runtime sockets, metadata services, and node-level services.
- Analyze cluster RBAC: over-privileged service accounts, escalation verbs (escalate, bind, impersonate), risky admission of workloads, and secrets readable from the namespace.
- Test cluster attack paths from a compromised pod: token abuse, API server access, lateral movement across nodes, and registry credential theft.
- Evaluate image and supply-chain posture: base image hygiene, image signing, admission control, and registry exposure.

Every test is performed with the agreed guardrails; nothing disrupts the running production workloads beyond the agreed intensity, and every access gained is documented and reported for rotation.$$,
 $$Workload and RBAC configuration are fully reviewed, escape vectors are tested hands-on from the agreed workloads, all confirmed escape or privilege paths are documented with evidence and severity, and hardening recommendations are produced.$$, NULL, '[]'::jsonb),

('Wireless Network Assessment', 'wireless network assessment',
 $$Assess the security of the in-scope wireless networks and radio interfaces.

Method:
- Survey the radio environment: enumerate networks, channels, signal coverage, client devices, and rogue or neighboring access points.
- Evaluate encryption and authentication: open networks, WEP, WPA/WPA2-PSK handshakes (capture and crack within the agreed intensity), WPA2/WPA3-Enterprise configuration, EAP types and certificate validation.
- Test client-side attacks permitted by the rules: evil twin and captive portal simulation in the controlled lab segment, karma-style probing, and deauthentication-based attacks only on the designated test networks.
- Assess Bluetooth/NFC/RFID surface where in scope: pairing modes, discoverability, relay and downgrade opportunities.
- Evaluate segmentation: whether guest and IoT wireless networks are properly isolated from the corporate network.
- Every attack is confined to the explicitly authorized SSIDs, BSSIDs and frequency ranges; out-of-scope networks and bystander devices are never touched.

All captures and cracking artifacts are stored as evidence with timestamps.$$,
 $$The radio environment is surveyed, the security posture of every in-scope network is measured, permitted attacks are executed and evidenced, segmentation is verified, and a report with hardening steps is produced.$$, NULL, '[]'::jsonb),

('Phishing Simulation and Social Engineering', 'phishing simulation and social engineering',
 $$Execute an authorized social engineering and phishing simulation against the target organization.

Method:
- Reconnaissance: gather public information about the organization, staff, technologies, events and language style to build credible pretext scenarios.
- Develop the scenario set agreed with the stakeholder: credential-harvesting phishing, malicious attachment simulation (benign payload), and smishing/vishing scripts where approved.
- Stand up the controlled infrastructure: sending domains, tracking, and a landing page that captures the interaction without storing real credentials in plaintext.
- Execute waves against the agreed recipient list; measure delivery, open, click, credential submission and report rates per department.
- Track every interaction with timestamps for the metrics report; all data stays within the engagement boundary and is deleted on request.
- Debrief: which indicators would have helped recipients, how reported mails were handled, and what detection fired.

Stay within the authorized recipient list and channels only; any interaction from outside the list is out of scope and stopped immediately. No real credentials are ever stored unencrypted, and no payload executes beyond the agreed benign marker.$$,
 $$The scenario set is executed against the full authorized recipient list, interaction metrics per wave are collected, the security awareness posture is quantified, and a debrief report with training recommendations is produced.$$, NULL, '[]'::jsonb),

('OSINT and External Footprint Reconnaissance', 'osint and external footprint reconnaissance',
 $$Perform deep passive and semi-passive OSINT against the target organization to map its external footprint.

Method:
- Enumerate domains, subdomains and hostnames via certificate transparency, DNS datasets, search engines and passive sources; resolve and fingerprint the live ones.
- Discover exposed assets: staging hosts, admin panels, dashboards, APIs, storage buckets, CI/CD artifacts, exposed documents, code leaks, and pasted credentials references.
- Map people and structure: public staff profiles, roles, email formats, organizational changes, and technology mentions in job postings.
- Analyze public code: repositories, gists, package artifacts and CI logs for secrets and internal details.
- Enumerate leaked credential exposure indicators through approved sources without logging into third-party services beyond the agreed access.
- Correlate everything into an attack-surface map with the technologies, versions and ownership of each asset.

Stay passive-first: no intrusive scanning beyond light service fingerprinting, no interaction that could alert the target beyond the agreed intensity, and strict scope adherence to the named organization and its known subsidiaries.$$,
 $$The external footprint is comprehensively mapped (domains, assets, technologies, people, exposure), all discoveries are documented with sources and confidence levels, and an attack-surface report with prioritized exposure findings is produced.$$, NULL, '[]'::jsonb),

('Source Code Security Audit', 'source code security audit',
 $$Perform a manual and tool-assisted security audit of the target source code repository or application.

Method:
- Map the codebase: entry points, trust boundaries (user input, network, file, IPC), authentication and authorization logic, session management, and data flows to sinks.
- Hunt the vulnerability classes relevant to the stack: injection (SQL, command, template, LDAP), deserialization, path traversal, SSRF, XSS, CSRF, race conditions, logic bugs, and insecure cryptography.
- Trace each untrusted input from source to sink; a suspected flow is confirmed only with a concrete exploitable path, including the attacker-visible trigger.
- Audit dependencies and build configuration: known CVEs with reachable paths, lockfile drift, CI/CD workflow abuse, and artifact provenance.
- Audit secrets handling: hardcoded credentials, key storage, logging of sensitive data, and secret rotation readiness.
- For each finding: exact file/line references, exploitability argument, worst-case impact and a concrete fix.

Produce findings as a ranked list with a proof-of-concept for every exploitable issue.$$,
 $$The full codebase is mapped and audited for the relevant vulnerability classes, every finding carries file/line evidence and exploitability analysis, dependencies and CI are audited, and a ranked findings report with concrete fixes is produced.$$, NULL, '[]'::jsonb),

('Full-Scope Red Team Operation', 'full-scope red team operation',
 $$Execute a full-scope, objective-driven red team operation against the target organization.

Method:
- Phase 1 - preparation: define objectives with the stakeholder, agree rules of engagement, blackout windows and emergency contacts; prepare covert infrastructure and operator OPSEC.
- Phase 2 - access: obtain the initial foothold through the agreed vector (external exploitation, phishing, physical or supply-chain as approved), keeping noise below detection thresholds where stealth is an objective.
- Phase 3 - operation: progress from the foothold toward the defined objectives via privilege escalation, lateral movement and credential abuse; persist as agreed; adapt to defensive responses in real time.
- Phase 4 - objective validation: reach and demonstrate the agreed objectives (data access, system control, business-process abuse) with minimal footprint and full evidence capture.
- Phase 5 - exfiltration simulation: demonstrate the ability to move objective data over the agreed covert channels within size limits.
- Maintain a complete operator log: every action, timestamp, technique (mapped to ATT&CK), artifact, and observed defensive response.

Strictly follow the rules of engagement: anything not explicitly permitted is forbidden, destructive actions require prior written approval, and the engagement can be aborted instantly on the emergency channel.$$,
 $$The agreed objectives are reached or credibly blocked, the complete attack narrative is documented end-to-end with ATT&CK-mapped techniques and evidence, detected vs undetected steps are analyzed, and an executive-plus-technical report with detection gaps and hardening priorities is produced.$$, NULL, '[]'::jsonb)
ON CONFLICT (nkey) DO NOTHING;
