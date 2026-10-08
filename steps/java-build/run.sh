#!/bin/bash
set -e
cd "$LP_WORKSPACE"
[ -n "$LP_PRE" ] && eval "$LP_PRE"
eval "$LP_COMMAND"
