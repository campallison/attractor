# Attractor

A Go implementation of the [Attractor specification](https://github.com/strongdm/attractor) -- a DOT-based pipeline runner that uses directed graphs to orchestrate multi-stage AI workflows.

Each node in the graph is a task (LLM call, conditional branch, etc.), each edge is a transition with optional conditions, and the engine traverses the graph deterministically from start to exit.

## What This Project Implements

This project implements all three layers of the Attractor spec:

**Layer 1 -- Unified LLM Client** (`internal/llm`): A minimal client that talks to [OpenRouter](https://openrouter.ai/) using the OpenAI-compatible Chat Completions API. Handles tool definitions, tool calls, structured error classification, and usage tracking.

**Layer 2 -- Coding Agent Loop** (`internal/agent`, `internal/tools`): An agentic loop that sends a prompt to the LLM, executes any tool calls the model requests (`read_file`, `write_file`, `edit_file`, `shell`), feeds results back, and repeats until the model is done. Shell commands run inside a Docker container for isolation.

**Layer 3 -- Pipeline Runner** (`internal/dot`, `internal/pipeline`): The Attractor-specific layer. Parses DOT files into directed graphs, validates them against lint rules, and executes them with handler dispatch, edge selection, goal gate enforcement, retry with backoff, checkpointing, and context management.

## Project Structure

```
attractor/
├── cmd/
│   ├── run-pipeline/        # Reference pipeline runner (pre-flight, execution, reporting)
│   ├── check-consistency/   # Static analysis tool for verifying generated code consistency
│   ├── check-behavioral/    # Behavioral validation tool (starts server, sweeps routes)
│   ├── test-llm/            # Smoke test for the LLM client
│   ├── test-agent/          # Smoke test for the agent loop
│   └── test-pipeline/       # Smoke test for the pipeline runner
├── internal/
│   ├── llm/                 # Layer 1: LLM client (types, client, errors, OpenRouter adapter)
│   ├── tools/               # Layer 2: Tool implementations (read, write, edit, shell, truncation)
│   ├── agent/               # Layer 2: Agent loop, system prompt, conversation compression
│   ├── dot/                 # Layer 3: DOT lexer, parser, and graph model
│   ├── pipeline/            # Layer 3: Execution engine, handlers, context, checkpoint, validation
│   ├── consistency/         # Static analysis checks for generated code (route-handler agreement, etc.)
│   ├── store/               # Observability: PostgreSQL persistence for run/stage/event data
│   └── logging/             # Structured logging setup (slog multi-handler)
├── pipelines/               # DOT pipeline definitions
├── go.mod
└── README.md
```

## Prerequisites

- **Go 1.21+**
- **Docker Desktop** (for the `shell` tool, which runs commands in an isolated container)
- **OpenRouter API key** (set `OPENROUTER_API_KEY` in a `.env` file at the project root)
- **PostgreSQL** (optional, for observability database — see below)

## Setup

```bash
git clone https://github.com/campallison/attractor.git
cd attractor

# Create a .env file with your OpenRouter API key
echo "OPENROUTER_API_KEY=sk-or-..." > .env

# (Optional) Start a local Postgres for observability data
docker run -d --name attractor-db -p 5432:5432 \
  -e POSTGRES_USER=attractor -e POSTGRES_PASSWORD=attractor -e POSTGRES_DB=attractor \
  -v attractor-pgdata:/var/lib/postgresql/data postgres:17

# Add the database URL to .env
echo "ATTRACTOR_DB_URL=postgres://attractor:attractor@localhost:5432/attractor?sslmode=disable" >> .env
```

## Running Tests

```bash
# Run all unit tests
go test ./...

# Run with verbose output
go test ./... -v

# Run tests for a specific package
go test ./internal/dot/ -v
go test ./internal/pipeline/ -v
```

All tests are table-driven and use [go-cmp](https://pkg.go.dev/github.com/google/go-cmp/cmp) for assertions. Tests do not require an API key or network access.

## Smoke Tests

These require a valid `OPENROUTER_API_KEY` in `.env` and (for the agent/pipeline tests) a running Docker daemon.

```bash
# Layer 1: Test the LLM client directly
go run ./cmd/test-llm

# Layer 2: Test the agent loop (creates a file via tool calls)
go run ./cmd/test-agent

# Layer 3: Test the pipeline runner (parses, validates, and executes a DOT pipeline)
go run ./cmd/test-pipeline
```

## Writing a Pipeline

Pipelines are defined as Graphviz DOT digraphs. A minimal example:

```dot
digraph MyPipeline {
    graph [goal="Implement a feature"]

    start [shape=Mdiamond]
    exit  [shape=Msquare]

    plan      [shape=box, prompt="Plan the implementation for: $goal"]
    implement [shape=box, prompt="Implement the plan", goal_gate=true,
               model="anthropic/claude-opus-4.6"]

    start -> plan -> implement -> exit
}
```

Key concepts:

- **Shapes determine behavior:** `Mdiamond` = start, `Msquare` = exit, `box` = LLM task, `diamond` = conditional routing
- **`$goal` expansion:** The `$goal` variable in prompts is replaced with the graph-level `goal` attribute
- **Per-node model:** Nodes can specify `model="provider/model-name"` to override the pipeline default
- **Goal gates:** Nodes with `goal_gate=true` must succeed before the pipeline can exit
- **Edge conditions:** Edges can have conditions like `condition="outcome=success"` to control routing
- **Retry:** Nodes support `max_retries` with exponential backoff
- **Build gates:** Nodes can specify `check_cmd="go build ./..."` to run a compilation check after each stage. If the check fails, the engine parses `[CHECK:name] PASS/FAIL` markers from the output and builds a structured retry prompt showing only failing checks' details. The agent is re-invoked up to `check_max_retries` times (default 3). For richer validation, chain `check-consistency` (static analysis) and `check-behavioral` (form-aware server startup + route sweep): `check_cmd="go build ./... && check-consistency && check-behavioral"`. The behavioral sweep parses HTML forms from templates and submits them with plausible dummy data, exercising server-side business logic beyond input validation guards.
- **Per-stage round limits:** Nodes can specify `max_rounds=25` to cap how many agent rounds a stage may run, preventing runaway stages from burning tokens
- **Usage tracking:** Each codergen stage writes a `usage.json` with token counts, and the pipeline aggregates totals in `RunResult`
- **Budget cap:** Set `MaxBudgetTokens` on `RunConfig` to halt the pipeline if cumulative token usage exceeds a threshold

## Running a Pipeline

The `run-pipeline` runner demonstrates all pipeline features:

```bash
# Real run (requires OPENROUTER_API_KEY in .env and Docker)
go run ./cmd/run-pipeline/ -pipeline pipelines/my-pipeline.dot -workdir /path/to/workdir

# Simulated run (no API key or Docker needed -- tests pipeline structure and logging)
go run ./cmd/run-pipeline/ -pipeline pipelines/my-pipeline.dot --simulate

# Cheap test run with a different model + zero data retention
go run ./cmd/run-pipeline/ -pipeline pipelines/my-pipeline.dot --model-override google/gemini-2.5-flash --zdr

# Disable prompt caching (on by default for Anthropic models)
go run ./cmd/run-pipeline/ -pipeline pipelines/my-pipeline.dot -workdir /path/to/workdir --prompt-cache=false

# Run with companion database for behavioral validation (starts per-run PostgreSQL)
go run ./cmd/run-pipeline/ -pipeline pipelines/my-pipeline.dot -workdir /path/to/workdir --companion-db
```

The runner performs a pre-flight checklist (workdir, API key, Docker, model validation against OpenRouter's API, budget sanity) before execution begins. When Docker is enabled, it cross-compiles and provisions the `check-consistency` and `check-behavioral` binaries into the sandbox container. The `--companion-db` flag starts a per-run PostgreSQL container on a shared Docker network, enabling behavioral validation stages to start the generated server and test its endpoints.

## Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Language | Go | Strong typing maps well to the spec's structured data; good HTTP client and concurrency primitives |
| LLM Provider | OpenRouter | Single API key for multiple model providers; OpenAI-compatible format means one adapter |
| Shell Security | Docker | Commands run in an isolated container, not on the host machine |
| Per-Run Sandbox | `attractor-<runID[:8]>` containers | Each pipeline run gets its own Docker container; tool registry is injected into the agent for clean dependency boundaries |
| DOT Parser | Hand-rolled | Full control over the spec's strict subset, custom attribute types, and error messages |
| Testing | Table-driven + go-cmp | Consistent patterns, readable diffs, easy to extend |
| Build Gates | `check_cmd` attribute | Compiler-enforced correctness between stages; catches cross-file inconsistencies early |
| Form-Aware Sweep | HTML form parsing + dummy data | `check-behavioral` extracts forms from templates, matches them to routes, and submits plausible form data; catches error-handling bugs hidden behind validation guards |
| Contract-First Design | Interface files from design stage | Downstream stages implement against shared Go interfaces, enforced by `go build` gates |
| Prompt Caching | Anthropic `cache_control` via OpenRouter | System/user messages cached at ~10% cost; reduces input token expense across multi-round agent loops |
| Context Carryover | Stage summaries injected into downstream prompts | Reduces redundant file reads by giving each stage a structured summary of what prior stages produced |
| Agent Methodology | Read-plan-implement-check in system prompt | Agents follow a structured sequence: read all relevant files, plan in `_scratch/plan.md`, implement in dependency order, validate via check commands instead of re-reading output |
| Working Memory | `_scratch/` directory with engine lifecycle | Agents maintain working notes; engine seeds context, verifies summary, archives, and cleans between stages |
| Behavioral Detection | Read-loop detection, nudge, escalation, empty-output | Detects 5+ consecutive read-only rounds, injects up to 2 course-correction nudges (resetting on writes), and terminates the stage early if the pattern persists after nudges are exhausted; warns when a stage produces no deliverable files |
| Failure Demand Tracking | Classify tool calls as value vs. failure demand | Each tool call is classified by heuristic (re-reading own files, rewriting same path = failure demand). Ratio logged at agent completion for pipeline analysis |
| Filesystem Observation | Directory snapshots + diff between stages | Ground-truth detection of what files an agent added, removed, or modified — independent of conversation reports; enhances empty-output detection and provides diffs for downstream context |
| Run-Scoped Context | `context.Context` threaded through all layers | Signal handling (SIGINT/SIGTERM) cancels runs in bounded time; deadlines and programmatic cancellation supported throughout |
| Observability Database | Local PostgreSQL via Docker | Optional persistence of run/stage/event data for cross-run analysis; `NopRecorder` when no DB configured |
| Structured Logging | `log/slog` multi-handler | Always-on INFO to terminal, DEBUG to JSON file; no toggle flag |

## Spec Reference

This implementation follows the three Attractor specifications:

- [Attractor Pipeline Spec](https://github.com/strongdm/attractor/blob/main/attractor-spec.md) (Layer 3)
- [Coding Agent Loop Spec](https://github.com/strongdm/attractor/blob/main/coding-agent-loop-spec.md) (Layer 2)
- [Unified LLM Client Spec](https://github.com/strongdm/attractor/blob/main/unified-llm-spec.md) (Layer 1)

## License

MIT
