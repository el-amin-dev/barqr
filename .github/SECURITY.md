# Security policy

## Reporting a vulnerability

Please report security issues through
[GitHub's private advisory form](https://github.com/el-amin-dev/barqr/security/advisories/new)
rather than a public issue.

Include the barqr version (`GET /v1/version`), the configuration involved
(`barqr check-config` output is safe to paste — it redacts API keys), and a
reproduction. You will get an acknowledgement within a few days.

## Supported versions

The latest minor release is supported. barqr follows semantic versioning and
does not break `/v1` within a major version.

## What is in scope

- Anything that lets an unauthenticated caller reach a rendering endpoint.
- Any input — request body, query, uploaded image, preset file — that causes a
  crash, an unbounded allocation, or a read outside the intended directory.
- Server-side request forgery through the remote-fetch feature.
- Leakage of API keys or payload contents into logs, errors, or responses.
  Payloads routinely contain Wi-Fi passwords and TOTP secrets.
- Container escapes or privilege escalation from the published image.

## What is out of scope

- Exposing barqr to the public internet and being abused. The service refuses
  to start in the two configurations most likely to cause this by accident,
  and the documentation is explicit; deliberately overriding those guards is a
  deployment choice, not a vulnerability. See
  [`docs/SECURITY.md`](../docs/SECURITY.md).
- Denial of service from a caller who already holds a valid API key. Rate
  limits and the concurrency semaphore bound this, but a trusted client is
  trusted.
- Scannability of a rendered code. A code that will not scan is a bug, not a
  vulnerability.

## Design commitments

These are properties barqr intends to hold, and a violation of any of them is
a security bug worth reporting:

- API keys are hashed at boot; the plaintext is never stored on the
  configuration, never logged, and never returned.
- Key comparison is constant time and evaluates every configured key.
- The process exits non-zero rather than serving unauthenticated on a wildcard
  bind, unless explicitly acknowledged.
- Errors returned to clients never contain stack traces, file paths,
  environment values, or internal type names.
- Uploaded images are size- and pixel-capped from their header before any
  pixel is decoded.
- The published image has no shell, no package manager, and runs as UID 65532.
