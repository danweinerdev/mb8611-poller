#!/bin/sh
set -eu

directory=${1:?usage: scrub-fixtures.sh DIRECTORY}
for fixture in "$directory"/*.json; do
  [ -f "$fixture" ] || continue
  # Preserve response shape while removing device/customer identifiers.
  sed -E -i \
	-e 's/[[:xdigit:]]{2}:[[:xdigit:]]{2}:[[:xdigit:]]{2}:[[:xdigit:]]{2}:[[:xdigit:]]{2}:[[:xdigit:]]{2}/XX:XX:XX:XX:XX:XX/g' \
    -e 's/("[A-Za-z]*MAC[A-Za-z]*"[[:space:]]*:[[:space:]]*")[^"]*/\1XX:XX:XX:XX:XX:XX/g' \
    -e 's/("[A-Za-z]*(Serial|CM MAC)[A-Za-z]*"[[:space:]]*:[[:space:]]*")[^"]*/\1REDACTED/g' \
    -e 's/("[A-Za-z]*(Customer|Version)[A-Za-z]*"[[:space:]]*:[[:space:]]*")[^"]*/\1REDACTED/g' \
    "$fixture"
done
