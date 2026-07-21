# Investigation workspace UX redesign

## Goal

Replace the feature-oriented inspector with the guided failed-run investigation described in [the investigation workspace design](../../design-docs/investigation-workspace.md), without weakening playback, replay, privacy, or evidence-discipline invariants.

## Traceability

AFR-ING-003, AFR-PLY-001, AFR-RPL-001–004, AFR-DIF-001–003, AFR-SEC-001–004; AFR-T01–T15; UX-001–UX-010.

## Progress

- [x] Audit the current desktop/mobile hierarchy, authentication failure, navigation, payload expansion, comparison placement, and job progress behavior.
- [x] Approve the investigation-workspace thesis, vocabulary, north-star metric, interaction rules, and public contract direction.
- [x] Implement session discovery, connection onboarding, compact shell, routing, and typed recovery states.
- [x] Implement the attention-ranked runs inbox with enriched summaries, filters, saved views, and responsive navigation.
- [x] Implement Understand with an incident brief, decision-oriented readiness, collapsed failure-path timeline, operation panel, and lazy evidence.
- [x] Implement Reproduce with a guided safe experiment builder, real job stages, job history, and comparison handoff.
- [x] Implement Compare with first-important-change priority, confidence/ambiguity, downstream grouping, and regression handoff.
- [x] Replace the synthetic flagship fixture with a real deterministic refund-review agent whose failed baseline, isolated replay, candidate capture, and diff are all produced through product contracts.
- [x] Pass automated responsive, keyboard, lazy-payload, privacy, browser-history, architecture, and live product-journey gates.
- [x] Replace the abstract three-stage UI with the explicit Add run → Inspect trace → Change value → Replay pipeline → Compare runs workflow.
- [x] Add one-value tool-output, model-output, and request-aware model-input overrides with typed API/SDK contracts and fail-closed replay behavior.
- [x] Verify edited request/response digests in candidate evidence before presenting an override as applied, and preserve earlier incidental replay drift separately.
- [ ] Run the external moderated usability benchmark and record completion rate, time-to-action, unsafe-replay misunderstandings, and SUS before the release gate is declared complete.

## Decisions

- The product is an investigation of one failed run, not a general observability dashboard.
- The user-facing terms are recorded run, replay readiness, controlled replay, and first important change; capsule terminology remains in versioned wire contracts.
- Backend contracts may grow additively where the UI otherwise would duplicate domain decisions or bulk-fetch payloads.
- Session tokens use session storage only. Non-sensitive filters and density may use local storage.
- Playback summaries omit payload bodies by default; verified bytes are fetched only on explicit request.
- The existing visual identity remains, but operational hero content and equal-weight evidence grids are removed.
- Evaluator authoring and bisect control remain CLI-first; the inspector supplies a regression handoff.
- Example data is harness-only. The production inspector has no fixture launcher, embedded demo token, seeded-project behavior, or example-specific defaults.
- A controlled UI experiment changes exactly one recorded boundary value. Tool/model output overrides are substituted; model input overrides require a request-aware live boundary callback. The original capsule remains immutable.

## Validation evidence

- Baseline browser audit at 1440x900 and 390x844 showed an authorization dead end, an oversized hero, a primary job-ID form, below-fold runs, stacked mobile master/detail behavior, fully expanded payload evidence, and comparison at the end of the page.
- `examples/investigation-agent` produces a developer example through real OpenTelemetry capture and SDK boundaries under the ordinary `refund-operations` project. The recorded baseline denies an eligible refund, the isolated replay changes only the declared model/prompt variant, and the candidate completes successfully while retrieval and tool responses stay frozen. The inspector has no knowledge of this example.
- Mocked browser journeys cover typed connection recovery, decision-first Understand, lazy payload retrieval, evidence/observation/hypothesis discipline, and durable stage progress without percentages. Live journeys exercise the actual agent investigation plus narrow incomplete/corrupt evidence fixtures.
- Responsive browser acceptance passes at 390x844, 768x1024, and 1440x900 with no horizontal overflow. Mobile opens on the incident decision rather than covering it with the selected-operation sheet; operation evidence opens only after an explicit action.
- `make verify` passed, including a separate isolated worktree that retained the real-agent source/candidate/diff, replay stages, live inspector DOM/trace/screenshots/video, incomplete-evidence proof, damaged hosted playback, privacy scans, and teardown evidence under `.artifacts/harness/20260721T132751Z-16950`.
- Each implementation slice must retain DOM, screenshot, trace, and video artifacts at 1440x900, 768x1024, and 390x844.
- Release evidence must prove the task, performance, accessibility, privacy, and usability criteria in the design document.
