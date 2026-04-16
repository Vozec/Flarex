#!/usr/bin/env bash
# ci_loop.sh — thin wrapper used by the test-and-document loop.
# Runs a FlareX command, captures stdout+stderr, anonymizes, writes to
# /tmp/flarex-run/<id>.out. The scenario expects the caller to pass
# explicit preconditions already arranged.
#
# Usage:
#   scripts/ci_loop.sh <scenario-id> -- <command> [args...]
#
# Example:
#   scripts/ci_loop.sh 1.1 -- ./bin/flarex list
#
# Notes:
# - Exit code is the COMMAND's exit code, not this script's.
# - Preserves ANSI colors — strip with `sed -r "s/\x1b\[[0-9;]*[mK]//g"` if
#   you want plaintext.
# - Output dir is /tmp/flarex-run (created if missing).
# - Does NOT manage preconditions or verification; that's the caller's job.

set -euo pipefail

if [[ $# -lt 3 || "$2" != "--" ]]; then
	echo "usage: $0 <scenario-id> -- <command> [args...]" >&2
	exit 2
fi

ID="$1"
shift 2

OUTDIR=/tmp/flarex-run
mkdir -p "$OUTDIR"
RAW="$OUTDIR/$ID.raw"
CLEAN="$OUTDIR/$ID.out"

HERE="$(dirname "$(readlink -f "$0")")"

# Run, capture both streams, stream to stdout too.
set +e
"$@" 2>&1 | tee "$RAW"
RC=${PIPESTATUS[0]}
set -e

# Anonymize into .out (safe to commit / embed in docs).
"$HERE/anonymize.sh" < "$RAW" > "$CLEAN"

echo "[ci_loop] id=$ID exit=$RC raw=$RAW clean=$CLEAN" >&2
exit $RC
