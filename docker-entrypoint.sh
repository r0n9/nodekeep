#!/bin/sh
set -e

mkdir -p /dashboard/data

if [ ! -f /dashboard/data/config.yaml ]; then
    cp /dashboard/defaults/config.yaml /dashboard/data/config.yaml
fi

exec "$@"
