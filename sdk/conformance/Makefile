# SDK Conformance Makefile — wraps the three harnesses into a single
# orchestration so CI + local runs speak the same commands.
#
# Usage:
#   make sim                     # build the gateway simulator binary
#   make conformance-go          # run the Go harness
#   make conformance-python      # run the Python harness
#   make conformance-typescript  # run the TypeScript harness
#   make conformance             # run all three + aggregate
#   make clean                   # delete generated reports + sim bin
#
# Everything writes JUnit XML into ./reports/ (gitignored); the
# aggregated markdown summary lives at reports/summary.md and is
# committed as an artifact so regressions show up in PR diffs.

SHELL := /bin/bash

SIM_DIR    := simulator
SIM_BIN    := $(SIM_DIR)/bin/cordum-gateway-sim
SIM_BIN_WIN := $(SIM_DIR)/bin/cordum-gateway-sim.exe
FIXTURES   := fixtures
REPORTS    := reports

.PHONY: all sim conformance conformance-go conformance-python \
        conformance-typescript aggregate clean validate-fixtures

all: conformance

# --- Simulator -----------------------------------------------------

sim: $(SIM_BIN)

$(SIM_BIN): $(shell find $(SIM_DIR) -name '*.go' 2>/dev/null)
	@echo "[sim] building simulator binary"
	@mkdir -p $(SIM_DIR)/bin
	@cd $(SIM_DIR) && go build -o bin/cordum-gateway-sim ./cmd/cordum-gateway-sim
	@if [ -f $(SIM_BIN_WIN) ] && [ ! -f $(SIM_BIN) ]; then cp $(SIM_BIN_WIN) $(SIM_BIN); fi

# --- Per-harness targets -------------------------------------------

conformance-go: sim
	@echo "[harness-go]"
	@cd harness/go && go run . \
		--sim-bin ../../$(SIM_BIN) \
		--fixtures ../../$(FIXTURES) \
		--report ../../$(REPORTS)/go.xml

conformance-python: sim
	@echo "[harness-python]"
	@cd harness/python && python -m conformance \
		--sim-bin ../../$(SIM_BIN) \
		--fixtures ../../$(FIXTURES) \
		--report ../../$(REPORTS)/python.xml

conformance-typescript: sim
	@echo "[harness-typescript]"
	@cd harness/typescript && node src/main.mjs \
		--sim-bin ../../$(SIM_BIN) \
		--fixtures ../../$(FIXTURES) \
		--report ../../$(REPORTS)/typescript.xml

# --- Aggregated matrix ---------------------------------------------

# `conformance` runs every harness then aggregates. Use `|| true`
# so a single harness failure still lets the aggregator render the
# full matrix — the aggregator itself exits non-zero if any SDK failed
# any fixture.
conformance: sim
	@mkdir -p $(REPORTS)
	@echo "[matrix] running all three harnesses"
	@$(MAKE) -s conformance-go || true
	@$(MAKE) -s conformance-python || true
	@$(MAKE) -s conformance-typescript || true
	@$(MAKE) -s aggregate

aggregate:
	@node ci/aggregate_reports.mjs \
		--go $(REPORTS)/go.xml \
		--python $(REPORTS)/python.xml \
		--typescript $(REPORTS)/typescript.xml \
		--out-md $(REPORTS)/summary.md \
		--out-json $(REPORTS)/summary.json

validate-fixtures:
	@echo "[validate-fixtures]"
	@node ci/validate_fixtures.mjs \
		--fixtures $(FIXTURES) \
		--schema schema/fixture.schema.json \
		--operation-spec ../../docs/api/openapi/cordum-api.yaml

clean:
	@rm -f $(SIM_BIN) $(SIM_BIN_WIN)
	@rm -f $(REPORTS)/*.xml $(REPORTS)/summary.json
