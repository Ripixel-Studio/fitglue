#!/usr/bin/env bash
# env-check.sh — compare ~/.jarvis/fitglue.env against deploy/box/fitglue.env.example.
#
# Prints key NAMES only, never a value. Exit 1 if a required key is missing or
# empty, 0 otherwise. Required = every uncommented KEY= line in the example
# that is not in OPTIONAL below. Keys in the env file that the example does
# not list are reported as info (the deployment keys the units also set are
# expected there, for fitgluectl shells).
#
#   deploy/box/env-check.sh                 # ~/.jarvis/fitglue.env
#   deploy/box/env-check.sh /path/to/file   # another file
#   deploy/box/env-check.sh --quiet         # only problems
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
example=$here/fitglue.env.example
envfile=$HOME/.jarvis/fitglue.env
quiet=0
for a in "$@"; do
  case $a in
    --quiet|-q) quiet=1 ;;
    -h|--help) sed -n 2,13p "$0"; exit 0 ;;
    *) envfile=$a ;;
  esac
done

# Keys the example lists that may legitimately be unset/empty. Keep in step
# with the comments in fitglue.env.example.
OPTIONAL="PUBSUB_PUSH_SERVICE_ACCOUNT API_URL WEB_URL SENTRY_DSN SENTRY_RELEASE PARKRUN_FETCHER_URL SITE_EXPORT_BUCKET SITE_EXPORT_DISABLED GOOGLE_CLIENT_ID GOOGLE_CLIENT_SECRET"

[ -r "$envfile" ] || { echo "FAIL: $envfile missing or unreadable" >&2; exit 1; }

mode=$(stat -c %a "$envfile")
[ "$mode" = 600 ] || echo "WARN: $envfile mode is $mode, expected 600 (chmod 600 $envfile)"

# Keys the example declares: uncommented and commented-out (`# KEY=`) alike;
# the commented ones are optional by construction.
example_keys=$(grep -oE '^#? ?[A-Z][A-Z0-9_]*=' "$example" | tr -d '# ' | tr -d '=' | sort -u)
example_required=$(grep -oE '^[A-Z][A-Z0-9_]*=' "$example" | tr -d '=' | sort -u)

is_optional() { case " $OPTIONAL " in *" $1 "*) return 0 ;; esac; return 1; }

# Parse the env file the way systemd does for our purposes: KEY=value lines,
# ignore blank and comment lines. Record presence and emptiness, never values.
declare -A present empty
while IFS= read -r line || [ -n "$line" ]; do
  case $line in ''|'#'*) continue ;; esac
  if [[ $line =~ ^([A-Z][A-Z0-9_]*)=(.*)$ ]]; then
    k=${BASH_REMATCH[1]}
    present[$k]=1
    [ -z "${BASH_REMATCH[2]}" ] && empty[$k]=1 || true
  else
    echo "WARN: unparseable line (not KEY=value): $(printf '%s' "$line" | cut -c1-20 | sed 's/=.*/=…/')"
  fi
done < "$envfile"

missing=() blank=() optional_unset=() extra=()
for k in $example_required; do
  if [ -z "${present[$k]:-}" ]; then
    is_optional "$k" && optional_unset+=("$k") || missing+=("$k")
  elif [ -n "${empty[$k]:-}" ]; then
    is_optional "$k" && optional_unset+=("$k") || blank+=("$k")
  fi
done
for k in "${!present[@]}"; do
  grep -qx "$k" <<<"$example_keys" || extra+=("$k")
done

status=0
if ((${#missing[@]})); then echo "MISSING (required, not in file): ${missing[*]}"; status=1; fi
if ((${#blank[@]}));   then echo "EMPTY   (required, no value):    ${blank[*]}";   status=1; fi
if ((quiet == 0)); then
  ((${#optional_unset[@]})) && echo "optional unset: $(printf '%s\n' "${optional_unset[@]}" | sort | tr '\n' ' ')"
  ((${#extra[@]})) && echo "not in example (unit-level keys are fine here): $(printf '%s\n' "${extra[@]}" | sort | tr '\n' ' ')"
  n_req=$(wc -w <<<"$example_required")
  echo "ok: $((n_req - ${#missing[@]} - ${#blank[@]} - $(for k in "${optional_unset[@]}"; do grep -qx "$k" <<<"$example_required" && echo x; done | wc -l))) of $n_req example keys set, ${#present[@]} keys in $envfile"
fi
[ $status = 0 ] && [ $quiet = 1 ] && echo "ok"
exit $status
