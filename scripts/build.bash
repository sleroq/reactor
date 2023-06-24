#!/usr/bin/env bash

set -e

SCRIPT=$(readlink -f "$0")
SCRIPTPATH=$(dirname "$SCRIPT")

cd "$(dirname "$SCRIPTPATH")"

go build -o dist/reactor src/*.go
