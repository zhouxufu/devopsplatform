#!/bin/bash
set -e
cd "$LP_WORKSPACE"
[ -n "$LP_INSTALL" ] && eval "$LP_INSTALL"
eval "$LP_COMMAND"
