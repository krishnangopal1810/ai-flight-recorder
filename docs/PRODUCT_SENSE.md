# Product sense

AI Flight Recorder helps an engineer reach a trusted next action on one failed agent run. It is not a trace viewer or a generic observability dashboard. Optimize for the time from opening a run until the user confidently inspects missing evidence, starts an isolated experiment, reviews the first aligned change, or preserves a fix as a regression.

Start with the decision the evidence supports. Recorded facts may describe where a run failed; computed differences may describe what changed; only controlled evidence may validate an explanation. Keep Evidence, Observation, and Hypothesis visually and semantically distinct, and never turn temporal correlation into causal language.

Playback is passive evidence inspection, never application execution. A verified manifest may remain useful when individual payloads are missing or corrupt, so retain the logical tree and unaffected facts, label each unavailable value at its original reference, and block stricter workflows instead of turning partial storage damage into an empty or misleading screen.

Developer demonstrations must come from a simple real agent using the same capture, storage, replay, isolation, and diff contracts as a user’s agent. The inspector must not expose fixture launchers, embedded demo credentials, or behavior keyed to example data. Synthetic data may prove damaged-storage and authorization states in automated tests, but it must not substitute for the core product story.
