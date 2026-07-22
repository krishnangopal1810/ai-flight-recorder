# Public alpha reader study

Run this study with at least five agent engineers who have not seen the repository or received a product explanation.

## Setup

- Give each reader only the repository URL on a fresh browser session.
- Do not narrate, scroll, or explain terminology.
- Ask them to think aloud without coaching.
- Record timestamps and exact answers, not an interpretation written later.
- For the demo, use a fresh macOS arm64 or Linux amd64 machine with Docker and no project toolchain.

## Questions

At 10 seconds, ask: **What problem does Flight Recorder solve?**

At 60 seconds, ask:

1. How is this different from a trace viewer?
2. What stays offline during replay?
3. Where did the refund demo first diverge?
4. What command would you run next?

Then let the reader run the Docker demo without maintainer intervention. Record time to the terminal comparison and time to the inspector result.

## Passing thresholds

- All five identify controlled reproduction of a failed agent run within 10 seconds.
- At least four identify the changed `refund-decision` model request within 60 seconds.
- All five say the retriever and eligibility tool remain recorded/offline during replay.
- All five select the Docker command and complete it without intervention.
- Nobody describes the product only as an observability dashboard.
- Nobody misses or materially misunderstands the safety and alpha limitations.

## Evidence table

| Reader | Background | 10s problem | 60s divergence | Offline boundaries | Demo complete | Time | Misconception |
|---|---|---|---|---|---|---|---|
| 1 | Pending | Pending | Pending | Pending | Pending | — | — |
| 2 | Pending | Pending | Pending | Pending | Pending | — | — |
| 3 | Pending | Pending | Pending | Pending | Pending | — | — |
| 4 | Pending | Pending | Pending | Pending | Pending | — | — |
| 5 | Pending | Pending | Pending | Pending | Pending | — | — |

A miss changes the README or onboarding path before launch. Do not average away a safety misconception.
