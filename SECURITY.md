# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in `go-ieee20305`, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please email **security@otfabric.io**, or open a private advisory on GitHub (the [Security](https://github.com/otfabric/go-ieee20305/security) tab → **Advisories** → **Report a vulnerability**), with:

- A description of the vulnerability
- Steps to reproduce the issue, ideally a minimal program or test
- The affected version(s)
- Any potential impact you've identified

We will acknowledge your report within 48 hours and aim to provide a fix or mitigation within 7 days for critical issues.

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest release | Yes |
| Older releases | Best effort |

## Security Considerations

### What IEEE 2030.5 messages do

DER controls carry power limits and operating modes that equipment acts on. Metering data reveals how a site uses energy. Whoever can issue controls can curtail or dispatch generation and storage; whoever can read metering data learns a site's consumption and production patterns.

### Transport: mutual TLS

IEEE 2030.5 requires HTTPS with mutual TLS (client certificate authentication). The `security` package provides the TLS configuration, LFDI/SFDI derivation, and certificate chain validation that the standard mandates.

### Device identity

Devices are identified by their certificate fingerprint. The LFDI (Long-Form Device Identifier) is the leftmost 160 bits of the SHA-256 hash of the device certificate. The SFDI (Short-Form Device Identifier) is derived from the same fingerprint. There are no passwords or bearer tokens.
