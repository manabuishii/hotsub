#!/bin/bash

PROJROOT=$(dirname $(dirname $(cd $(dirname $0) && pwd)))

set -e -v

hotsub run \
    --tasks ${PROJROOT}/test/wordcount/empty-task.csv \
    --script ${PROJROOT}/test/wordcount/main.sh \
    --verbose
