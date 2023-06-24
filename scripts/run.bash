#!/usr/bin/env bash

set -e

SCRIPT=$(readlink -f "$0")
SCRIPTPATH=$(dirname "$SCRIPT")

cd "$(dirname "$SCRIPTPATH")"

./scripts/build.bash

source ./scripts/env.bash

./dist/reactor
