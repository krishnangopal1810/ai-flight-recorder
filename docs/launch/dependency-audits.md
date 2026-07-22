# Dependency audit policy

The public-alpha release gate audits every shipped dependency tree. Go uses `govulncheck` v1.6.0 plus `scripts/check-go-licenses.sh`; JavaScript uses `npm audit --omit=dev`; Python exports all frozen production and optional dependencies and passes the hash-pinned requirements to `pip-audit` v2.10.1 without dependency re-resolution.

`scripts/check-sdk-licenses.py` separately fails closed on every non-development package in the three npm lockfiles and every frozen Python production or optional dependency. Missing, unknown, copyleft, or otherwise unapproved license expressions block release.

The current Go graph has no vulnerable symbols or imported packages. `govulncheck` reports one module-only advisory for the unmaintained `golang.org/x/crypto/openpgp` package; the product does not import or link that package, and the advisory has no upstream fixed version. This residual is recorded in the alpha audit rather than suppressed.

## Narrow Go license exception

`google/go-licenses` v1.6.0 does not classify `modernc.org/mathutil` v1.7.1 even though that module archive contains a standard BSD-3-Clause `LICENSE` and BSD license headers. The Go package index also classifies v1.7.1 as BSD-3-Clause.

The audit script ignores only that module after asserting both the reviewed version and the exact Git object hash of its license text. Any module or license-text change fails the release gate and requires a fresh review. This exception does not allow an unknown license family.
