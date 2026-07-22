# Public alpha audit report: v0.1.0-alpha.1

Date: 2026-07-22
Verdict: **Not yet authorized for publication**

The implementation and local technical gates are ready for review. Publication is still blocked by fresh Linux and published-image proof, the five-reader study, green CI on the exact release commit, repository configuration, and explicit owner confirmation.

## Passed locally

- `make verify` passed after the dependency upgrades. The fresh-worktree evidence is retained at `.artifacts/harness/20260721T194646Z-54187`; all build, unit, integration, security, replay, browser, generation-drift, architecture, docs, and quality gates passed.
- The real Docker launcher completed the refund incident on macOS arm64 with local release-equivalent images: `deny → approve`, first divergence `refund-decision → model request`, and exactly the retriever and eligibility-tool substitutions.
- Repeated `start`, `status`, plain `stop`, restart, and `stop --purge` preserved the token/result when expected and left no labelled containers, networks, or volumes after purge.
- `scripts/scan-history-secrets.py` passed its detection fixtures and scanned 515 reachable Git blobs plus 255 worktree files with no findings.
- `govulncheck` v1.6.0 reports zero vulnerable symbols and zero vulnerable imported packages after upgrading `x/crypto`, `x/net`, `x/sync`, `x/sys`, and `x/text`.
- All three npm production audits and the frozen Python production-plus-optional audit report zero known vulnerabilities.
- The license gate passed for the Go graph, 23 production Node packages, and 16 frozen Python packages. The sole Go-tool exception is pinned to the exact `modernc.org/mathutil` v1.7.1 BSD-3-Clause license content.
- README QA passed 19 narrative headings, all local links and anchors, useful alt text, a 9.6-second 2.9 MB GIF, static fallback, 1280×640 social preview, and the 60-word pacing limit.

## Recorded residual

`govulncheck` reports GO-2026-5932 against the `golang.org/x/crypto` module because its legacy `openpgp` subpackage is unmaintained. The project does not import or link `openpgp`, no symbol or imported-package vulnerability is reachable, and the advisory has no upstream fixed version. The release audit preserves this result in verbose output instead of suppressing it.

## Still blocking publication

- Commit the implementation, push it, and obtain green `main` CI at that exact commit.
- Run the Docker-only command from the published multi-architecture images on fresh Linux amd64 and fresh macOS arm64 hosts, including the five-minute threshold.
- Complete the five unfamiliar-engineer reader study without coaching and meet every comprehension threshold.
- Build, attach, verify, and sign the real CLI archives, checksums, archive SBOMs, and all three published images. The release workflow now keeps the GitHub release in draft until those checks and the live published-image demo pass.
- Verify the README on signed-out GitHub desktop and mobile rendering.
- Apply the reviewed repository description, topics, social preview, Discussions categories, branch protection, vulnerability reporting, package visibility, and real `good first issue` records.
- Obtain explicit owner confirmation immediately before creating the tag or changing repository visibility.

No release tag, package publication, repository-visibility change, or external repository-setting mutation has been performed by this implementation pass.
