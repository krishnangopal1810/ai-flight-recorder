# V1 acceptance matrix

`make verify` is the blocking gate. The named tests below make every PRD acceptance ID queryable, but an ID is not considered complete until [the V1 evidence audit](V1_EVIDENCE.md) links it to a full deterministic journey at the scope of the PRD scenario. Lower-layer regressions remain useful evidence but do not substitute for end-to-end proof.

| Test | Executable evidence | Expected invariant |
|---|---|---|
| AFR-T01 | `TestAFRT01ThroughT03DiffSurfacesCausalInputDrift/AFR-T01` | Prompt input drift precedes the changed tool choice. |
| AFR-T02 | `TestAFRT01ThroughT03DiffSurfacesCausalInputDrift/AFR-T02` | Retrieval response drift aligns and remains causal evidence. |
| AFR-T03 | `TestAFRT01ThroughT03DiffSurfacesCausalInputDrift/AFR-T03` | Tool schema/request drift is surfaced before the tool failure. |
| AFR-T04 | `TestAFRT04RecordedExternalResponseIsSubstitutedOffline`, bundle E2E | Recorded response is substituted with no live host and bundles preserve identity offline. |
| AFR-T05 | `TestAFRT05RepeatedTrialsNeverClaimFalseDeterminism` | Repetitions produce exact, equivalent, divergent, or inconclusive. |
| AFR-T06 | `TestAFRT06PlaybackSurvivesMissingReplayEvidence` | Playback stays ready while replay names the missing boundary. |
| AFR-T07 | `TestAFRT07MalformedSpanIsIsolatedFromValidTrace` | A malformed span is rejected without discarding valid trace evidence. |
| AFR-T08 | `TestAFRT08SeededSecretsNeverReachLocalStore` | Policy runs before store, manifest, or quarantine persistence. |
| AFR-T09 | `TestAFRT09MutatingLiveBoundaryFailsPreflight` | Mutating live boundaries are blocked by default. |
| AFR-T10 | `TestAFRT10SandboxedMutationRequiresDeclarationAndFlag` | A declared sandbox still requires the explicit side-effect flag. |
| AFR-T11 | `TestAFRT11InternalInstrumentationDoesNotBecomeDivergence` | Added internal instrumentation does not become the causal divergence. |
| AFR-T12 | `TestAFRT12DirtyIdentityIncludesBinaryDiffAndSortedUntrackedFiles` | Binary diff and sorted untracked digests identify dirty source. |
| AFR-T13 | `TestAFRT13BisectValidatesMonotonicHistory` | Midpoint search returns the first bad revision with evaluator/diff evidence after exhaustive validation. |
| AFR-T14 | `TestAFRT14BisectRefusesNonMonotonicClaim`, `TestAFRT14BisectReturnsInconclusiveForFlakyRevision` | Non-monotonic or inconclusive history never yields a false first bad. |
| AFR-T15 | `TestAFRT15EvaluatorRunsInProviderAndPersistsEvidence` | The same versioned evaluator fails the source, passes the fix, and persists both results. |

Additional gates include property-like canonical identity checks, shared local/hosted repository contracts, OTLP late-span assembly, seeded-secret scans, bundle integrity, Python/TypeScript boundary matching tests, and Playwright playback/divergence/job journeys.
