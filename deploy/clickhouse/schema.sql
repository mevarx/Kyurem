-- Kyurem M2 ClickHouse bootstrap.
--
-- The Collector's ClickHouse exporter owns the trace table schema
-- (`otel.otel_traces`) and creates it on first export when
-- `create_schema: true` is set in deploy/otel-collector/config.yaml. This
-- script only guarantees the database exists for a fresh volume; creating
-- the table by hand here would drift from the exporter's nested
-- Events/Links layout.
--
-- Retention is applied by the exporter (`ttl: 168h`, seven days) to match
-- the demo default in PRD §6.2. Per-task materialized views arrive with the
-- M3 dashboard; M2 is scoped to one trace tree per task.

CREATE DATABASE IF NOT EXISTS otel;
