CREATE TABLE IF NOT EXISTS pipeline_runs (
    id                  UUID PRIMARY KEY,
    started_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at         TIMESTAMPTZ,
    duration_ms         INT,
    status              TEXT NOT NULL DEFAULT 'running',
    failure_reason      TEXT,
    pipeline_file       TEXT NOT NULL,
    graph_name          TEXT,
    goal                TEXT,
    default_model       TEXT,
    model_override      TEXT,
    simulate            BOOLEAN NOT NULL DEFAULT FALSE,
    docker_image        TEXT,
    budget_tokens       INT,
    total_input_tokens  INT DEFAULT 0,
    total_output_tokens INT DEFAULT 0,
    total_tokens        INT DEFAULT 0,
    stages_completed    INT DEFAULT 0,
    stages_total        INT DEFAULT 0
);

CREATE TABLE IF NOT EXISTS stage_results (
    id                      SERIAL PRIMARY KEY,
    run_id                  UUID NOT NULL REFERENCES pipeline_runs(id),
    node_id                 TEXT NOT NULL,
    sequence                INT NOT NULL,
    model                   TEXT,
    status                  TEXT NOT NULL,
    failure_reason          TEXT,
    exhaustion_reason       TEXT,
    rounds                  INT DEFAULT 0,
    input_tokens            INT DEFAULT 0,
    output_tokens           INT DEFAULT 0,
    total_tokens            INT DEFAULT 0,
    duration_ms             INT,
    prompt_length           INT,
    response_length         INT,
    files_added             INT DEFAULT 0,
    files_modified          INT DEFAULT 0,
    files_removed           INT DEFAULT 0,
    files_unchanged         INT DEFAULT 0,
    scratch_summary_produced BOOLEAN DEFAULT FALSE,
    build_gate_attempts     INT DEFAULT 0,
    build_gate_passed       BOOLEAN
);

CREATE TABLE IF NOT EXISTS stage_events (
    id          SERIAL PRIMARY KEY,
    stage_id    INT NOT NULL REFERENCES stage_results(id),
    run_id      UUID NOT NULL REFERENCES pipeline_runs(id),
    event_type  TEXT NOT NULL,
    round       INT,
    detail      TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_stage_results_run_id ON stage_results(run_id);
CREATE INDEX IF NOT EXISTS idx_stage_events_stage_id ON stage_events(stage_id);
CREATE INDEX IF NOT EXISTS idx_stage_events_run_id ON stage_events(run_id);
