# Contributing

Thanks for looking. barqr is small on purpose and intends to stay that way —
please read [the non-goals](#non-goals) before opening a large pull request.

## Getting set up

You need **Docker**. A local Go toolchain is optional: `make` detects one and
falls back to pinned containers when it is missing.

```bash
git clone https://github.com/el-amin-dev/barqr && cd barqr
make help      # every target, and which toolchain it will use
make ci        # build · vet · lint · test -race · docker build · smoke
```

`make ci` is the gate. It is exactly what the pull-request workflow runs, so a
green laptop and a green CI mean the same thing. Run it before you push.

## The shape of a change

barqr is four registries behind one HTTP surface:

| To add | Write | Register in |
|---|---|---|
| a symbology | `internal/encoder/<name>.go` | `encoder.Register` |
| a payload type | `internal/builder/<name>.go` | `builder.Register` |
| an output format | `internal/writer/<name>.go` | `writer.Register` |
| a module or eye shape | `internal/render/…` | `render.RegisterModuleShape` / `RegisterEyeShape` |

Each is **one new file plus one `init()` registration**. If your change needs a
`switch` in the HTTP layer, or a new field threaded through four packages, stop
and open an issue first — the design is probably fighting you, and that is
worth discussing before you write it.

## What the review looks for

1. **Doc comments on every exported symbol**, starting with its name. `revive`
   fails the build otherwise.
2. **Comments that explain why, not what.** `internal/encoder/qr.go` is the
   house style: the comment that earns its place records the spec rule being
   implemented, the reason for a threshold, or the trap being avoided.
3. **Errors wrapped with `%w`**, against the sentinel errors each package
   exports. Error strings are lowercase, unpunctuated, and actionable: say what
   was expected and what arrived.
4. **No panic in a request path.** The recover middleware is a backstop, not a
   strategy. Panic only for programmer errors that input cannot cause, such as
   a duplicate registration.
5. **No global mutable state** except the registries, populated in `init` and
   read-only after.
6. **Table-driven tests**, covering the failure paths. Coverage is expected to
   stay above 80% in `builder`, `encoder`, and `writer`.
7. **Nothing user-visible leaks internals.** No stack traces, file paths,
   environment values, or Go type names in a response.

## Payloads are secrets

A barqr payload may be a Wi-Fi password or a TOTP seed. Never log payload
contents, never echo them in an error, and never add a debug path that does.
This is not negotiable and it is checked in review.

## Golden files

Rendered output is asserted against committed references. When a change is
*intentionally* visual, regenerate them and say so in the pull request:

```bash
go test ./... -update
```

Review the diff before committing it. A regenerated golden file that nobody
looked at is a test that no longer tests anything.

## Commits and branches

- Short-lived branches off `main`, named `<type>/<slug>`.
- [Conventional commits](https://www.conventionalcommits.org): `feat:`, `fix:`,
  `docs:`, `test:`, `build:`, `ci:`, `refactor:`, `perf:`, `chore:`. The release
  changelog is generated from them.
- Write the body for the reader who runs `git log` in a year and needs to know
  *why*.

## Non-goals

These are settled, and a pull request adding one will be declined with thanks:

- a web UI or dashboard
- user accounts, tenancy, or billing
- a database in the core
- image editing beyond rendering a code
- a heavyweight framework
- CGO in the default build
- anything described as machine learning
- a breaking change to `/v1`

Optional symbologies that genuinely need a C library belong behind the `full`
build tag, registered as unavailable in the default build so
`/v1/symbologies` stays honest.

## Reporting a vulnerability

Do not open a public issue. See [`SECURITY.md`](../.github/SECURITY.md).
