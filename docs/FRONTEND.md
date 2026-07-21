# Frontend

The inspector is a read-oriented React application using only the generated OpenAPI client. Its stable direct routes cover capsule discovery, playback, completeness, job progress, and divergence evidence. Capsule detail consumes the typed playback projection rather than loading blobs or assembling unvalidated response maps in the UI.

Playback renders the verified logical operation tree, timings, inputs and outputs, model configuration, tool definitions, boundary matching and side-effect policy, artifacts, evaluations, tokens, cost, and usage. Each referenced payload has an explicit `available`, `missing`, `corrupt`, or `unavailable` state. Missing evidence remains visible as a warning beside its reference; digest-invalid bytes are never rendered. Completeness renders every missing prerequisite with its stable code and boundary ID rather than summarizing only the first warning. Evaluator authoring and bisect control remain CLI-first.

Use semantic HTML, accessible roles, stable `data-testid` attributes only where roles are insufficient, and deterministic seeded data. Playwright captures DOM snapshots, screenshots, traces, and video for critical journeys, including live partial playback after hosted object loss and corruption.
