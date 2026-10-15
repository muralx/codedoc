#!/usr/bin/env bash
set -euo pipefail

export JAVALIB_HOME=/opt/javalib
PYLIB_MODE="${LIB_MODE:-dev}"
echo "Calling $API_URL with $1 and $PYLIB_MODE"
