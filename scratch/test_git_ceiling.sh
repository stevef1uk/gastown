#!/usr/bin/env bash
mkdir -p /tmp/test-town/subdir
cd /tmp/test-town
git init
cd subdir
echo "At subdir:"
git rev-parse --show-toplevel
echo "With ceiling=/tmp/test-town:"
GIT_CEILING_DIRECTORIES=/tmp/test-town git rev-parse --show-toplevel
echo "With ceiling=/tmp/test-town/subdir:"
GIT_CEILING_DIRECTORIES=/tmp/test-town/subdir git rev-parse --show-toplevel
