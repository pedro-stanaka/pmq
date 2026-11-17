#!/usr/bin/env sh
# One-command local smoke test: disposable PMQ and a mocked AI provider.
set -eu
cd "$(dirname "$0")/.."
go test ./cmd/pmq -count=1 -v -run '^(TestFullLifecycleSmoke|TestDeploySkillWorkflow)$'
