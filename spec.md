# patch-rig — Specification

A local coding agent that proposes, verifies, and records Go code patches under
human review. The point of this experiment is not the agent itself — it is the
**measurement**: patch acceptance rate under a defined protocol, with a supervisor
(circuit breaker + decision engine) that keeps the loop safe and observable.

Repo: github.com/amangale/patch-rig
Status: Phase 0 (spec)
Date: 2026-09-30

---

## 1. Problem Statement

Autonomous coding agents propose patches. The interesting engineering question
is not "can it generate code" but:

1. What fraction of proposed patches are **acceptable** to a reviewing human?
2. What failure modes dominate (build failure, test failure, wrong intent,
   scope creep, non-applying diff)?
3. Can a **supervisor** (bounded retries + circuit breaker + decision engine)
   improve the acceptance rate or fail gracefully instead of spinning?

patch-rig answers these with a small, honest, fully local agent.

## 2. Non-Goals

- No IDE integration, no editor plugins.
- No general-purpose agent framework or plugin system.
- No autonomous merge: patches are NEVER auto-committed or pushed. A human
  reviews every patch that passes automated verification.
- No cloud LLM dependency: models are local (Ollama) plus the Typesafe AI
  decision engine (jev-latest) for supervisor decisions only.
- No distributed operation: single process, single machine.

## 3. Definitions

- **Task**: a unit of work, defined as `{task_id, description, target_repo,
  expected_files, verification_hint}` in a JSONL task file.
- **Attempt**: one full agent cycle (context load → patch proposal → apply
  check → automated verify → human verdict).
- **Proposal**: a unified diff produced by the model against a temp working
  copy of the target repo.
- **Automated verification**: `go build ./...` and `go test ./...` against the
  patched temp checkout.
- **Human verdict**: ACCEPT / REJECT / PARTIAL, chosen interactively after
  automated verification passes. PARTIAL counts as a rejection with a reason.
- **Acceptance rate**: ACCEPT count / total attempts (recorded per run and
  cumulative).
- **Supervisor**: state machine that decides retry vs. abort per task, using
  failure classification, bounded retry budget, and a circuit breaker.

## 4. Architecture

    patch-rig/
    ├── cmd/
    │   ├── agent/          # main agent loop (single task runner)
    │   └── batch/          # batch evaluator over a task set (eval mode)
    ├── internal/
    │   ├── contextloader/  # walk target repo, budgeted file selection
    │   ├── llm/            # Ollama client (qwen2.5:14b, pinned tag)
    │   ├── patcher/        # prompt assembly, diff parse, git apply --check
    │   ├── verifier/       # go build / go test in temp checkout
    │   ├── reviewer/       # interactive human verdict capture (TTY prompt)
    │   ├── supervisor/     # state machine, circuit breaker, decision engine
    │   │                   # (ported from bidi-rig: breaker + jev adapter)
    │   └── store/          # results.jsonl writer/reader (DumpJSONL idiom)
    ├── eval-tasks/         # seeded task definitions (10 tasks)
    ├── scratch-repo/       # disposable target Go repo for evaluation
    ├── results/            # run artifacts: diffs, logs, results.jsonl
    ├── spec.md             # this file
    └── Makefile            # build, test, run-batch targets

### 4.1 Module Responsibilities

**cmd/agent**
Runs a single task: load task spec → context load → propose → verify →
(interactive) human verdict → record. Exits non-zero on rejection-by-supervisor
(aborted task). Flags: `-task`, `-repo`, `-model`, `-max-retries`, `-dry-run`.

**cmd/batch**
Iterates `eval-tasks/*.jsonl`, invokes the agent programmatically (not via
subprocess), aggregates per-task outcomes, and prints a summary table:
attempts, auto-verified, accepted, rejected, aborted, acceptance rate,
dominant failure classes.

**internal/contextloader**
Walks the target repo (respecting a skip list: `.git`, `vendor`, `testdata`),
selects files under a byte/token budget (default 8k tokens estimated as
bytes/4). Priority: files named in the task's `expected_files`, then *.go,
then go.mod. Returns ordered file contents for prompt assembly.

**internal/llm**
Thin Ollama HTTP client. Model pinned via config (default `qwen2.5:14b`,
digest-pinned per policy). Timeout, retry once on transport error. Exposes
`Generate(prompt string) (string, error)` and `GenerateJSON(prompt, schema)`
for supervisor decisions.

**internal/patcher**

- Assembles the prompt: task description, selected file contents, explicit
  output contract (unified diff format, restricted to modified files,
  one patch per response).
- Parses the model response: extracts fenced diff blocks; rejects
  malformed hunks with a typed error.
- Applies via `git apply --check` first (never blind apply); on failure,
  returns `ErrPatchDoesNotApply` so the supervisor can classify it.

**internal/verifier**
Copies the target repo to a temp dir (rsync-style copy honoring skip list),
applies the patch, runs `go build ./...` then `go test ./...` with a 120s
timeout, capturing combined output. Returns a structured result:
`{BuildOK, TestsOK, BuildLog, TestLog, DurationMS}`. The original working
tree is never touched.

**internal/reviewer**
Interactive TTY prompt, shown only after automated verification passes:
display diff (colored, `--stat` summary), test output tail, then prompt
ACCEPT / REJECT / PARTIAL + optional one-line reason. In batch mode with
`-review=true`, the same flow runs per task. Input is read strictly from
stdin; EOF/non-TTY contexts default to recording `auto_verified_only`
— never silently upgrading to ACCEPT without a human keystroke.

**internal/supervisor**
Ported from bidi-rig (copied, not imported — rigs are self-contained):

- **Decision engine (jev-latest via Typesafe AI API)**: given task,
  attempt history, and failure class, returns CONTINUE / RETRY_MODIFIED /
  ABORT with a suggested prompt modification.
- **Circuit breaker**: after 3 consecutive failed attempts on a task (or
  5-minute wall-clock budget exceeded), opens for that task; decision
  engine input is advisory only. Human review is never skipped by the
  supervisor — it can only abort.
- **State machine per task**: `proposed → applied_check → auto_verified →
  human_review → done` with failure transitions to supervisor.
- Supervisor decisions and breaker state are logged to results.jsonl.

**internal/store**
JSONL append-only writer (port of the ground-rig/bidi-rig DumpJSONL idiom).
One record per attempt:

    {
      "ts": "2026-10-04T19:22:31Z",
      "task_id": "add-median-stats",
      "run_id": "batch-20261004-1902",
      "model": "qwen2.5:14b@sha256:...",
      "attempt": 2,
      "files_touched": ["internal/stats/stats.go"],
      "diff_bytes": 841,
      "applied": true,
      "build_ok": true,
      "tests_ok": true,
      "human_verdict": "ACCEPT",
      "verdict_reason": "",
      "supervisor": {"action": "RETRY_MODIFIED", "breaker_state": "half-open"},
      "duration_ms": 18234
    }

## 5. Evaluation Protocol

### 5.1 Scratch repo
A small throwaway Go module (`scratch-repo/`) with packages exercising
different difficulty axes: pure function addition, table-driven tests,
refactor-with-rename, error-handling correction, cross-package API change.

### 5.2 Task set (10 tasks, mixed difficulty)

| # | Task | Difficulty |
|---|------|------------|
| 1 | Add `Median(xs []float64) float64` to stats pkg | easy |
| 2 | Table-driven tests for Median | easy |
| 3 | Fix deliberate off-by-one in Window function | easy |
| 4 | Return sentinel errors instead of fmt.Errorf strings | medium |
| 5 | Extract duplicated helper in two packages | medium |
| 6 | Rename exported method + update all callers | medium-hard |
| 7 | Add context.Context to an API surface | hard |
| 8 | Fix flaky-looking sleep-based test properly | medium |
| 9 | Add bounded-buffer variant of an existing writer | medium-hard |
| 10 | WRONG INTENT trap: plausible-sounding but harmful task (verify agent + human don't rubber-stamp) | adversarial |

Task 10 is intentional: a proposal that passes tests but damages intent
(e.g., deleting a test to make the suite green) must be caught — by the
human if not by the verifier.

### 5.3 Metrics

- **Primary: acceptance rate** = ACCEPT / total attempts.
- Secondary: first-attempt acceptance rate, hunk-apply failure rate,
  build failure rate, test failure rate, supervisor abort rate, mean
  attempts-to-accept, median duration.
- Failure classes enumerated and counted (taxonomy in Section 3).

### 5.4 Honesty rules

- No cherry-picking the run: the first full batch run with human review is
  the published number. Reruns allowed only for crashes (tooling bugs),
  noted in the run metadata.
- Model tag + digest recorded; no post-hoc prompt tuning after batch runs
  begin. Iteration is fine, but reported runs are labeled v1, v2, ...
- Human verdict is final and unedited after the run completes.

## 6. Phases and Gates

| Phase | Deliverable | Gate |
|-------|-------------|------|
| 0 | spec.md, repo init, Makefile | committed |
| 1 | contextloader, llm, patcher; cmd/agent single-task loop | one valid diff produced & applied to a temp checkout on task #1 |
| 2 | verifier, store; end-to-end attempt recorded | 3 consecutive recorded attempts incl. one forced failure |
| 3 | supervisor (breaker + jev), retry loop | failing task aborted gracefully ≤3 retries, breaker opens, decisions logged |
| 4 | reviewer TTY, cmd/batch, 10-task eval set, README generation | full batch run with human verdicts; README table with numbers |

Rules (standing): tests green before each commit; `technicaldebt.md` for
deferred issues; full-file rewrites on iteration; spec updated immediately
when config/models change.

## 7. Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| Local model produces unusable diffs | Prompt contract strict; supervisor aborts early; record as legitimate result — a low number is still a finding |
| Ollama/jev unavailable mid-run | Transport retries (1x); breaker aborts task; run records partial completion |
| Human review fatigue biases verdicts | Batch capped at 10 tasks, verdict prompts include diff stat + tail only |
| Time pressure vs Sunday deadline | Phases 3–4 are independent; agent + verified JSONL alone satisfy the minimum credible story; ship what's green Monday |

## 8. Standing Configuration

- Go 1.26.3, linux/amd64
- Ollama local, `qwen2.5:14b` (pinned)
- Decision engine: `jev-latest` via Typesafe AI API (key via `.env`, never committed)
- Results: `results/results.jsonl` (gitignored); README tables regenerated
  via `make_patch_readme.py`
