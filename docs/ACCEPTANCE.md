# V1 acceptance matrix

`make verify` is the blocking gate. The named tests below make every PRD acceptance ID queryable, but an ID is not considered complete until [the V1 evidence audit](V1_EVIDENCE.md) links it to a full deterministic journey at the scope of the PRD scenario. Lower-layer regressions remain useful evidence but do not substitute for end-to-end proof.

| Test | Executable evidence | Expected invariant |
|---|---|---|
| AFR-T01 | `TestAFRT01ThroughT03DiffSurfacesCausalInputDrift/AFR-T01` | Prompt input drift precedes the changed tool choice. |
| AFR-T02 | `TestAFRT01ThroughT03DiffSurfacesCausalInputDrift/AFR-T02` | Retrieval response drift aligns and remains causal evidence. |
| AFR-T03 | `TestAFRT01ThroughT03DiffSurfacesCausalInputDrift/AFR-T03` | Tool schema/request drift is surfaced before the tool failure. |
| AFR-T04 | `scripts/reference-replay-smoke.sh`, `scripts/typescript-reference-smoke.sh`, `TestAFRT04RecordedExternalResponseIsSubstitutedOffline` | Python and TypeScript recorded responses are substituted; the Python mixed trial permits only the declared model host and both all-substituted trials attach no egress network. |
| AFR-T05 | `TestAFRT05RepeatedTrialsNeverClaimFalseDeterminism` | Repetitions produce exact, equivalent, divergent, or inconclusive. |
| AFR-T06 | `scripts/completeness-smoke.sh`, `TestAFRT06PlaybackSurvivesMissingReplayEvidence` | A live missing-tool-response capsule stays playable, blocks replay and bisect with the exact boundary ID through API/CLI/UI, and fails replay preflight without execution. |
| AFR-T07 | `TestAFRT07MalformedSpanIsIsolatedFromValidTrace` | A malformed span is rejected without discarding valid trace evidence. |
| AFR-T08 | `scripts/privacy-smoke.sh`, `TestAFRT08SeededSecretsNeverReachLocalSinks`, `TestAFRT08SeededSecretsNeverReachHostedSinks` | API keys, tokens, passwords, configured email PII, exclusions, and strict failures are policy-processed before canonical request hashing, local/hosted storage, indexes, bundles, quarantine, diagnostics, logs, or metrics. |
| AFR-T09 | `TestAFRT09MutatingLiveBoundaryFailsPreflight` | Mutating live boundaries are blocked by default. |
| AFR-T10 | `TestAFRT10SandboxedMutationRequiresDeclarationAndFlag` | A declared sandbox still requires the explicit side-effect flag. |
| AFR-T11 | `TestAFRT11InternalInstrumentationDoesNotBecomeDivergence` | Added internal instrumentation does not become the causal divergence. |
| AFR-T12 | `TestAFRT12DirtyIdentityIncludesBinaryDiffAndSortedUntrackedFiles` | Binary diff and sorted untracked digests identify dirty source. |
| AFR-T13 | `TestAFRT13BisectValidatesMonotonicHistory` | Midpoint search returns the first bad revision with evaluator/diff evidence after exhaustive validation. |
| AFR-T14 | `TestAFRT14BisectRefusesNonMonotonicClaim`, `TestAFRT14BisectReturnsInconclusiveForFlakyRevision` | Non-monotonic or inconclusive history never yields a false first bad. |
| AFR-T15 | `TestAFRT15EvaluatorRunsInProviderAndPersistsEvidence` | The same versioned evaluator fails the source, passes the fix, and persists both results. |

Additional gates include property-like canonical identity checks, shared local/hosted repository contracts, scheduled project retention with race-safe CAS deletion and content-free audits, class-aware keep/omit/truncate limits with visible completeness, a complete transport-independent OTLP/OpenInference capsule with malformed-span partial success, OTLP late-span assembly, seeded-secret scans, bundle integrity, non-loopback native/proxy TLS with scoped HTTP/gRPC authorization, Python/TypeScript boundary matching tests, and Playwright playback/divergence/job journeys. AFR-PLY-001 is blocked by `scripts/playback-offline-smoke.sh` and `scripts/playback-smoke.sh`: playback must start zero application processes, make zero outbound requests, reject direct reads of corrupt bytes, and retain verified unaffected evidence after hosted payload loss and corruption.
