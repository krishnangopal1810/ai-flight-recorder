# Public alpha launch execution plan

Status: Active
Owner: Maintainer
Last verified: 2026-07-22

## Traceability

AFR-CLI-001, AFR-SVC-001, AFR-RPL-001–003, AFR-DIF-001–003, AFR-SEC-001–004, AFR-PLY-001; AFR-T01–T15.

## Progress

- [x] Replace the terminology-led README with the refund-incident story, real animation, one-command payoff, and compact technical disclosures.
- [x] Package a Docker-only launcher with `start`, `status`, `stop`, `stop --purge`, named isolation, custom loopback port, and stable JSON output.
- [x] Make recorded retriever and tool callbacks fail if they are invoked during the controlled replay.
- [x] Formalize the recorder mark, inspector favicon, README media, and 1280×640 social preview.
- [x] Add contribution, conduct, security, roadmap, community, and issue-intake surfaces.
- [x] Pass the complete post-upgrade `make verify`, history secret scan, dependency vulnerability audits, and Go/Node/Python license gates locally.
- [x] Prove the real launcher lifecycle and controlled refund replay on macOS arm64 with local release-equivalent images.
- [ ] Prove the launcher end to end on fresh macOS arm64 and Linux amd64 Docker engines.
- [ ] Verify signatures, SBOMs, archives, and the live journey against the published release artifacts.
- [ ] Conduct the five-reader comprehension and unassisted demo study.
- [ ] Obtain explicit owner confirmation immediately before release publication and visibility change.

## Decisions

- Docker is the only canonical alpha onboarding path. Registry publication for Python, TypeScript, and Homebrew remains deferred.
- The launcher calls the existing real refund agent, OTLP receiver, replay worker, and diff API; it does not add a fixture-only product path.
- Every Docker resource carries both the global demo label and the user-selected demo name. Cleanup selects both labels and never deletes images or unrelated resources.
- Plain `stop` preserves evidence volumes and the last result. Destructive evidence cleanup requires the explicit `--purge` flag.
- The demo network is Docker-internal. The inspector is published only on `127.0.0.1`; replayed applications keep the existing per-job isolation contract.
- The repository stays private until the final audit report is reviewed and the owner confirms publication.

## Validation evidence

- `scripts/demo-launcher-test.sh` proves command parsing, JSON status, explicit purge, and empty labelled cleanup behavior without a Docker daemon.
- `examples/investigation-agent/agent.py` throws if either recorded retriever/tool callback executes when `FLIGHT_REPLAY_PLAN` is present. A completed candidate is therefore executable offline-substitution evidence.
- `scripts/update-readme-media.sh` renders the checked-in 9.6-second GIF and three static beats from retained passing end-to-end browser evidence; it blocks GIFs at or above 5 MB.
- `scripts/render-social-preview.py` deterministically renders the checked-in 1280×640 PNG from the formalized mark and launch story.
- `scripts/scan-history-secrets.py` scans every reachable Git blob and the current worktree offline, prints only redacted finding locations, and self-tests exact fixture allowlists.
- Post-upgrade `make verify` passed with fresh-worktree evidence retained at `.artifacts/harness/20260721T194646Z-54187`.
- The local audit reports zero vulnerable Go symbols/imported packages, zero npm/Python vulnerabilities, and allowed licenses for the Go graph, 23 Node production packages, and 16 frozen Python packages. See `docs/launch/audit-report-v0.1.0-alpha.1.md` for the recorded module-only residual.
- Full Docker, cross-platform, reader, release, and publication evidence remains pending and blocks the public tag or visibility change.
