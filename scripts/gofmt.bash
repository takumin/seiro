#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$(dirname "${SCRIPT_DIR}/../..")" && pwd)"

cd "${PROJECT_DIR}"

gofmt -w .
