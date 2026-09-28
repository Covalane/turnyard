#!/bin/sh
set -eu
if [ "$#" -ne 1 ]; then
	printf 'usage: stamp <value>\n' >&2
	exit 2
fi
if [ "${TURNYARD_EXEC_WITNESS_MODE:-}" != "enabled" ]; then
	printf 'missing witness environment\n' >&2
	exit 3
fi
printf 'EXEC_WITNESS:%s\n' "$1"
