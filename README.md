# patch-rig

A local coding agent that proposes, verifies, and records Go code patches
under **human review** — built to measure one thing: *what fraction of
AI-generated patches is actually acceptable to a reviewing engineer?*

**Status:** early phase — Phase 1 gate passed, Phase 2+ (automated verifier,
batch evaluator, supervisor) in development.

## Why this exists

Most conversations about AI coding agents celebrate generation. This
experiment asks a different question: when an autonomous agent proposes a
patch, what actually survives scrutiny? Acceptance rate, not cleverness, is
the metric that matters to an engineering organization.

patch-rig is deliberately small and honest:

- **Local models only** (Ollama, pinned tags + digests) — no cloud LLM dependency.
- **Human verdicts are final.** Nothing is ever auto-committed. No keystroke,
  no accept.
- **Failures are findings.** Every non-applying diff, failed build, and
  intent violation is recorded in an append-only JSONL store and published.

## What it does

Given a task definition and a target repository, patch-rig:

1. Loads repository context under a byte budget (priority: expected files,
   then `*.go`, then module files).
2. Asks a local model (default: `qwen2.5:7b`) to respond with **complete
   file contents** per a strict output contract.
3. Materializes those files in a disposable git workspace and generates the
   diff with git itself — the model is never asked to hand-write hunk
   line numbers, because that is incidental complexity, not capability.
4. Records the attempt (`applied`, file list, duration, model digest) to
   `results/results.jsonl` for later verification and human verdicts.

Planned: automated verification (`go build` / `go test` against the
workspace), a supervisor with circuit breaker + decision engine for
retry-vs-abort, an interactive reviewer, and a 10-task evaluation batch
(including an adversarial wrong-intent trap).

## Early findings (published, not curated)

The first attempts produced two instructive failures — this is the data:

| Attempt | Model | Outcome |
|---|---|---|
| 1 | qwen2.5:14b | **NON_APPLYING_DIFF** — model hand-wrote a unified diff with hallucinated hunk line counts; patch rejected by `git apply --check`. Contract redesigned: full-file output, git computes the diff. |
| 2 | qwen2.5:7b | **INTENT_VIOLATION** — clean diff, build green, tests green... and the patch **deleted three existing functions** (`Sum`, `Mean`, `Window`) while adding the requested `Median`. Automated gates were completely blind to the loss; only human review caught it. |

**First-attempt acceptance rate after human review: 0/2.**

That number is the point. The second failure is the interesting one:
every automated gate passed while the change silently destroyed
functionality — because the surviving test suite only covered the newly
added function. Automated verification without coverage of existing
behavior is not verification; it is ceremony. Human review of intent
remained the only effective gate, exactly as the protocol predicted.

## Usage

    make build
    ./bin/agent -task eval-tasks/task01.jsonl -repo scratch-repo
    # inspect results/results.jsonl and results/*.txt

Requires: Go 1.26+, git, a local Ollama serving `qwen2.5:7b`.

## Repository layout

    cmd/agent/            single-task runner
    internal/contextloader/  budgeted repo file selection
    internal/llm/         Ollama client (pinned models)
    internal/patcher/      prompt assembly, response parsing, workspace + diff
    internal/store/        JSONL attempt records
    eval-tasks/           task definitions
    scratch-repo/          disposable evaluation target
    spec.md                full specification, phases, and honesty rules

See `spec.md` for the evaluation protocol, metric definitions, and the
rules that keep the published numbers honest: first full batch run is the
published number, model digests recorded, no post-hoc prompt tuning on
already-reported runs.

## License

MIT (see LICENSE).
