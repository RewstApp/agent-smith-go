#!/usr/bin/env bash
# Sends one command to the agent through the Rewst trigger webhook and
# classifies the engine's answer, so the step that fails is the step that
# actually failed and its annotation names the cause.
#
# This used to be a bare curl whose response was discarded. That reported
# success whenever the HTTP call itself succeeded - including when the engine
# answered without ever dispatching to the device - and the next step then
# polled the agent log for a result that could not arrive, pointing the
# investigator at agent code that never ran (sc-115631). Retrying the engine's
# own timeout came next (sc-115785). What remained (sc-117883) was that every
# other non-2xx was treated as a refusal on the first attempt - a single nginx
# 502 or a transient 404 "Workflow was not found" from the engine's front door
# failed a 25-minute run - and that any 2xx body was accepted, so a result
# belonging to another command (two agents sharing a device_id) passed here
# and failed the log assertion after it with a misleading message.
#
# Every outcome is one of these classes, named in the annotation:
#
#   request-failed    curl itself failed (connection refused, DNS, --max-time).
#                     Nothing was dispatched. Not retried: this is the runner's
#                     network, not a transient the engine will recover from.
#   engine-timeout    HTTP 408, or a 2xx carrying the engine's "did not complete
#                     in a reasonable amount of time" payload. The engine gave
#                     up at its own ceiling before dispatching. Retried; allowed
#                     through when allow_engine_timeout is set.
#   engine-transient  Any 5xx, or a 404 whose body says "Workflow was not
#                     found". The engine's front door failed before the request
#                     reached the workflow. Retried on the same schedule.
#   engine-refusal    Any other non-2xx. The request itself was refused (bad
#                     trigger URL, secret, or payload). Not retried.
#   wrong-result      2xx, but the body is not the device's postback for this
#                     command: no command_results object, or its output does
#                     not contain EXPECTED_OUTPUT. Another agent on the same
#                     device_id is the usual cause. Not retried.
#   success           2xx with a command_results object (and matching output).
#
# Inputs arrive as environment variables from action.yml. The file is separate
# from action.yml so test/sendcommand can run it against stub servers.
set -uo pipefail

: "${TRIGGER_URL:?}" "${DEVICE_ID:?}" "${COMMANDS:?}"
MAX_TIME="${MAX_TIME:-300}"
ALLOW_ENGINE_TIMEOUT="${ALLOW_ENGINE_TIMEOUT:-false}"
RETRY_DELAY="${RETRY_DELAY:-10}"
EXPECTED_OUTPUT="${EXPECTED_OUTPUT:-}"

fail() { echo "::error title=send-command: $1::$2"; exit 1; }
warn() { echo "::warning title=send-command: $1::$2"; }

# Body and status come back on one stream - curl appends the status as a final
# line - rather than via --output to a temp file. On windows-latest this runs
# under Git Bash, where a mktemp path is an MSYS path that the curl on PATH may
# not resolve; keeping everything on stdout behaves identically on all three.
send_once() {
  curl_exit=0
  response="$(
    curl --silent --show-error --location \
      --max-time "$MAX_TIME" \
      --write-out '\n%{http_code}' \
      --form "device_id=\"$DEVICE_ID\"" \
      --form "commands=$COMMANDS" \
      "$TRIGGER_URL"
  )" || curl_exit=$?
  http_code="$(printf '%s' "$response" | tail -n 1)"
  body="$(printf '%s' "$response" | sed '$d')"
}

have_jq=false
command -v jq >/dev/null 2>&1 && have_jq=true
# Test hook: test/sendcommand exercises the grep fallback on hosts that have jq.
[ "${SEND_COMMAND_FORCE_NO_JQ:-}" = "1" ] && have_jq=false

# command_results.output from a 2xx body, or empty when absent. With jq the
# value is JSON-decoded; without it (not expected on a hosted runner) the raw
# JSON-escaped string is returned, which still contains the plain words the
# suite matches on.
result_output() {
  if [ "$have_jq" = "true" ]; then
    printf '%s' "$1" | jq -r '.output.output.command_results.output // empty' 2>/dev/null
  else
    printf '%s' "$1" \
      | grep -oE '"command_results"[[:space:]]*:[[:space:]]*\{[^}]*' \
      | grep -oE '"output"[[:space:]]*:[[:space:]]*"([^"\\]|\\.)*"' \
      | head -n 1 \
      | sed -E 's/^"output"[[:space:]]*:[[:space:]]*"//; s/"$//'
  fi
}

attempts="${RETRY_ATTEMPTS:-3}"
case "$attempts" in ''|*[!0-9]*|0) attempts=1 ;; esac

for attempt in $(seq 1 "$attempts"); do
  send_once

  # Always surface the body: the flake triage playbook reads the preceding
  # send's payload out of the job log.
  echo "Response body (attempt $attempt of $attempts): ${body}"

  if [ "$curl_exit" -ne 0 ]; then
    fail "request-failed" "curl exited $curl_exit after at most ${MAX_TIME}s; the command was not dispatched to the device. This is the runner's connection, not an engine response, so it is not retried."
  fi

  # The engine gave up before dispatching. Two shapes at its ceiling: a 408, and
  # a 2xx carrying the timeout payload. Detected before the generic branches so
  # both honour allow_engine_timeout and the retry.
  engine_timed_out=false
  [ "$http_code" = "408" ] && engine_timed_out=true
  printf '%s' "$body" | grep -qF 'did not complete in a reasonable amount of time' && engine_timed_out=true

  if [ "$engine_timed_out" = "true" ]; then
    # Whitespace after the colon is tolerated; first match wins. Diagnostic
    # only - nothing branches on it.
    execution_id="$(
      printf '%s' "$body" \
        | grep -oE '"execution_id"[[:space:]]*:[[:space:]]*"[^"]*"' \
        | head -n 1 \
        | sed -E 's/.*"([^"]*)"$/\1/'
    )"

    if [ "$ALLOW_ENGINE_TIMEOUT" = "true" ]; then
      echo "::notice title=send-command: engine-timeout (expected here)::the engine gave up before the command finished (HTTP $http_code, execution_id=${execution_id:-unknown}). Continuing because allow_engine_timeout is set - this scenario needs the command still running on the device."
      exit 0
    fi

    if [ "$attempt" -lt "$attempts" ]; then
      warn "engine-timeout, retrying" "attempt $attempt of $attempts did not dispatch (HTTP $http_code, execution_id=${execution_id:-unknown}); retrying in ${RETRY_DELAY}s. A single crossing of the engine's ceiling does not fail the run."
      sleep "$RETRY_DELAY"
      continue
    fi

    fail "engine-timeout" "the engine did not dispatch the command in any of $attempts attempts (last: HTTP $http_code, execution_id=${execution_id:-unknown}). This is an engine-side timeout, NOT a regression in the code under test - re-dispatch rather than investigating the diff."
  fi

  # The engine's front door failed before the request reached the workflow: a
  # gateway 5xx, or the routing 404 the engine emits transiently for a trigger
  # that exists (seen on runs 35649657962 and 35410153035, each once, each on
  # a trigger that had just succeeded).
  engine_transient=false
  case "$http_code" in 5??) engine_transient=true ;; esac
  if [ "$http_code" = "404" ] && printf '%s' "$body" | grep -qF 'Workflow was not found'; then
    engine_transient=true
  fi

  if [ "$engine_transient" = "true" ]; then
    if [ "$attempt" -lt "$attempts" ]; then
      warn "engine-transient, retrying" "attempt $attempt of $attempts got HTTP $http_code from the engine before the command was dispatched; retrying in ${RETRY_DELAY}s. Engine-side, not the code under test."
      sleep "$RETRY_DELAY"
      continue
    fi
    fail "engine-transient" "the engine answered HTTP $http_code on all $attempts attempts and the command was never dispatched. This is an engine-side gateway or routing failure, NOT a regression in the code under test - re-dispatch rather than investigating the diff."
  fi

  # Any other non-2xx is a refusal of the request itself, not a transient.
  case "$http_code" in
    2??) ;;
    *) fail "engine-refusal" "the Rewst engine returned HTTP $http_code and the command was not dispatched to the device. A refusal (bad trigger URL, secret, or payload) is not retried." ;;
  esac

  # 2xx: make sure this is the device's postback for this command, not an
  # unrelated execution the engine happened to return.
  if ! printf '%s' "$body" | grep -qF '"command_results"'; then
    fail "wrong-result" "HTTP $http_code but the body carries no command_results object, so this is not the device's postback for this command. Check whether another agent shares this device_id (overlapping runs) or the engine returned an unrelated execution."
  fi

  if [ -n "$EXPECTED_OUTPUT" ]; then
    actual_output="$(result_output "$body")"
    case "$actual_output" in
      *"$EXPECTED_OUTPUT"*) ;;
      *) fail "wrong-result" "HTTP $http_code with command_results.output $(printf '%q' "$actual_output"), which does not contain the expected $(printf '%q' "$EXPECTED_OUTPUT"). The engine returned a result, but not this command's - another agent on the same device_id is the usual cause." ;;
    esac
  fi

  echo "send-command: dispatched (HTTP $http_code, attempt $attempt of $attempts, class: success)"
  exit 0
done
