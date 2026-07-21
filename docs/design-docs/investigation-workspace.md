# Investigation workspace

Status: Active
Owner: Product engineering
Last verified: 2026-07-21

## Product thesis

AI Flight Recorder is a guided scientific investigation of one failed agent run. It is not a generic trace viewer, dashboard, or capsule catalog. Its primary workflow is:

`Add a run -> Inspect trace -> Change one value -> Replay pipeline -> Compare runs -> Save as a regression`

The north-star metric is median **time to trusted next action**: from opening a recorded run until the engineer confidently inspects missing evidence, starts an isolated replay, compares a candidate, tests a change, or creates a regression handoff.

## Interaction model

The working vocabulary is deliberately small:

- **Recorded run** — what happened.
- **Replay readiness** — whether the run can be reproduced safely.
- **Controlled replay** — what happened after changing one declared thing.
- **First important change** — where the candidate began behaving differently.

Capsules, manifests, digests, boundary occurrences, alignment internals, and raw payload state remain available under technical details. Every normal investigation uses three levels of disclosure: decision summary, supporting evidence, and raw technical data. Evidence, computed observations, and hypotheses retain distinct types and labels.

The workspace exposes five numbered steps: **Add run**, **Inspect trace**, **Change value**, **Replay pipeline**, and **Compare runs**. Inspect maps to the understand route; change and replay share the reproduce route but have distinct editor and job states. Selected operation, boundary, field, job, filters, baseline, candidate, and open technical panel are deep-linkable and survive back/forward navigation.

## Required experiences

### Connect

A fresh session shows connection controls, never a raw authorization error. The user can test a service URL, enter a session-only token, see detected scopes, or use advertised loopback local mode. The production inspector contains no demo credentials, fixture launchers, seeded-project behavior, or project-specific defaults. Developer and test harnesses may populate the service with real agent runs, but those runs enter through the normal capture API and appear through the same inbox and investigation routes as every other run. Errors distinguish unreachable service, missing or invalid token, insufficient scope, and incompatible server. Tokens never enter URLs, persistent storage, logs, metrics, traces, or screenshots.

### Capture

The Add Run page begins with the outcome and the user's situation, not instrumentation terminology: the user will run the agent once and Flight Recorder will open a visual model/tool timeline; the agent may be on the current computer, already running elsewhere, or available as a recording file. The local path asks whether tracing already exists before showing a command. If it does not, the page gives a compact Python auto-instrumentation quickstart; it never presents a `flight record` command as sufficient for an untraced process. The already-running path copies the trace destination, authorization, and capture/project marker together, explains each setting in plain language, and warns users who are unsure to use the local path. Copying a setup starts listening for the next run, which opens automatically.

Normal OpenTelemetry traces are enough to inspect what happened. Boundary setup is collapsed under the goal “Need to change a model or tool value and replay this run?” and states that it must happen before recording only when safe replay is required. The expanded explanation shows one minimal example and explains the substituted-tool behavior. Import verifies the bundle and opens the resulting run automatically.

Every run can be exported from its investigation header with **Download recording**. The import screen teaches the inverse workflow in product terms: open Runs, select a run, download the recording, then choose that `.tar.zst` file. The CLI command is a secondary disclosure. Digest trees and content-addressed layout remain implementation details; the UI explains the guarantee as “complete and unchanged.”

### Find

The authenticated landing page is a compact runs inbox. It provides search, project/service/time filters, saved views, and a default attention order: recent failures, incomplete runs, unresolved replays, successful runs, then older runs. Rows show outcome, failure point, time, duration, replay readiness, evidence warnings, and candidate count; technical IDs appear only behind copy controls.

### Understand

The first viewport contains the run outcome, identity, replay readiness, deterministic incident brief, one recommended action, a compact passive-playback safety proof, and the start of the execution timeline. The failure path is expanded and selected; unrelated successful branches are collapsed. Operation details use a side panel or mobile sheet. Payload bytes are fetched only after explicit disclosure.

### Reproduce

Replay readiness states the decision first: ready or blocked, the exact missing prerequisites, what they block, what remains available, and how to resolve them. The normal experiment begins on a selected trace operation. Users may change a tool output, model output, or request-aware model input by editing verified recorded JSON. Exactly one boundary value changes per replay. The plan labels that value as Changed, substituted tools as Recorded, model calls as Live read, and unsafe mutations as Blocked before offering **Replay from this change**. Code revision, image, and model-variant changes remain an advanced path.

Blocked replay copy is operation-specific. It names the affected call, describes the missing input/output, explains the safety consequence in plain language, and gives a concrete capture-and-record-again fix. “Boundary response missing,” “default substitution,” raw completeness messages, and stable error codes remain in a collapsed Technical details disclosure. Recovery actions take the user to the affected timeline operation or the Add Run flow.

Job progress uses real named stages instead of invented percentages. Completion links directly to the resulting comparison; opening a job by UUID is a secondary recovery action.

### Compare and resolve

Comparison opens with an execution graph. It names the agent, condenses high-confidence matched operations before the split, and then follows recorded-run and candidate-replay lanes through the first changed value and up to three later changed steps. Each lane shows concise field-level values and ends with its explicit run outcome; the graph becomes a stacked, labelled sequence on narrow screens. Readable before/after details remain immediately below it. Expanding a value shows a field-by-field comparison with changed fields first and unchanged context collapsed; raw JSON requires a second, explicitly technical disclosure. Embedded JSON strings are decoded into readable fields for comparison without changing the recorded bytes. Matching evidence is collapsed under the user question “How were these values matched?” and first explains the shared operation, compared value, and match result in plain language. Algorithms, numeric scores, capsule IDs, and evidence fields require a nested technical disclosure. Later effects use plain operation-specific titles such as “Model response changed,” show at most three meaningful field-level changes, and translate high-confidence alignment to “Same operation matched.” The diff engine does not generate generic causal speculation, and the inspector renders explanations only when controlled evidence marks them as validated. A passing candidate closes the loop with a plain-language “Save as regression” action and CLI/evaluator handoff; evaluator authoring and bisect control remain CLI-first.

## Content and visual rules

- Every screen has one dominant action and explains the consequence of every status.
- Use sentence case and plain language; use monospace only for code and identifiers.
- Body text is at least 14px, secondary text at least 12px, and touch targets at least 44px.
- Red means actionable failure, amber means incomplete or uncertain, and green means verified success. Status never depends on color alone.
- Use a warm neutral canvas, white investigation surfaces, deep green actions, and restrained status colors. Dense dark surfaces are reserved for code and raw payloads rather than the entire application.
- Keep the global shell quiet and predictable. The current location uses a soft filled state; secondary utilities never compete with the page action.
- Put the primary task before controls: search and saved investigation views stay visible, while project, service, time, readiness, and sorting filters use progressive disclosure.
- Use large type only for the page decision, subtle shadows only to separate interactive surfaces, and consistent 10–16px radii instead of decorative borders around every section.
- Never present raw JSON as a one-line change summary. Lead with the changed concept and field values; keep complete payloads behind explicit disclosure.
- Never show full before/after JSON panes as the first expanded comparison. Show changed fields, collapse unchanged context, and place raw bytes one disclosure deeper.
- Never present matching algorithms, percentages, digest labels, or evidence counts as a user-facing explanation. Lead with the shared operation and compared value; place matching internals under technical details.
- Meet WCAG 2.2 AA, including keyboard navigation, visible focus, skip navigation, logical headings, accessible tree relationships, reduced motion, live progress announcements, and dialog focus management.

The following patterns are prohibited: operational hero sections, raw backend errors, a primary job-UUID form, fake percentages, four equal completeness cards as the default, inline expansion of every payload, color-only state, zero-result loading flashes, comparison below a long evidence page, generic primary actions, and success without a next action.

## Product-design benchmarks

The visual system borrows product principles, not brand styling, from three public references:

- [Apple](https://www.apple.com/) — one clear message, strong type hierarchy, generous whitespace, and few competing actions.
- [Airbnb](https://www.airbnb.com/) — lead with the task, use approachable controls, and keep secondary refinement lightweight.
- [LangSmith trace views](https://docs.langchain.com/langsmith/view-traces) — preserve investigation context while separating scan, turn summary, and technical debugging detail.

These benchmarks are applied to Flight Recorder's own evidence and replay workflow. Their logos, component styling, copy, and brand conventions are not copied.

## Success criteria

- At least 90% unassisted completion by representative AI-infrastructure engineers.
- Median time to trusted next action under 60 seconds.
- Failure point found in under 60 seconds and replay readiness understood in under 30 seconds.
- First important change found in under three minutes.
- Zero unsafe-replay misunderstandings and zero payload content in operational analytics.
- SUS above 85 and no serious or critical automated accessibility violations.

Implementation and validation are tracked in [the active investigation workspace plan](../exec-plans/active/investigation-workspace.md).
