#!/usr/bin/env bash
# anonymize.sh : stdin → stdout filter that replaces sensitive strings
# from your local FlareX dev environment with deterministic placeholders.
#
# Personal patterns live in scripts/anonymize.local.sh (gitignored). It
# defines a SED_LOCAL array of -e args. The template below applies
# generic regex replacements; the local file adds your specific token,
# account ID, subdomain, email, etc.
#
# Quick setup:
#   cp scripts/anonymize.local.sh.example scripts/anonymize.local.sh
#   # edit with your values
#   some_cmd | scripts/anonymize.sh
#
# Idempotent on its own output.

set -euo pipefail

LOCAL="$(dirname "$0")/anonymize.local.sh"
declare -a SED_LOCAL=()
if [ -f "$LOCAL" ]; then
	# shellcheck source=/dev/null
	source "$LOCAL"
fi

sed -r \
	-e 's/\x1b\[[0-9;]*[mK]//g' \
	-e 's/cfut_[A-Za-z0-9]{40,}/cfut_<redacted-token>/g' \
	-e 's/2a09:bac5:[0-9a-f:]*/<egress-ipv6>/g' \
	-e 's/2606:4700:[0-9a-f:]*/<egress-ipv6>/g' \
	-e 's/2a06:98c0:[0-9a-f:]*/<egress-ipv6>/g' \
	"${SED_LOCAL[@]}"
