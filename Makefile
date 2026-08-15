# Repo-level checks. Every target here is what CI runs — if it passes locally it passes there.
#
#   make setup    once per machine
#   make check    everything, in the order a reviewer would want it

VENV    := .venv
PYTHON  := $(VENV)/bin/python
PIP     := $(VENV)/bin/pip

.PHONY: setup fmt fmt-check validate-fixtures check clean

setup: $(VENV)/.installed
	npm install

$(VENV)/.installed: scripts/requirements.txt
	python3 -m venv $(VENV)
	$(PIP) install --quiet --upgrade pip
	$(PIP) install --quiet -r scripts/requirements.txt
	touch $@

fmt:
	npx prettier --write .

fmt-check:
	npx prettier --check .

# Structure against fixture.schema.json, plus the cross-file properties a schema cannot
# express — seed names, principal keys, and {{capture}} resolution.
validate-fixtures: $(VENV)/.installed
	$(PYTHON) scripts/validate_fixtures.py

check: fmt-check validate-fixtures

clean:
	rm -rf $(VENV) node_modules
