#!/bin/sh
# Entrypoint of the codedoc test image. The host tree is mounted read-only at
# /src; it is copied into the writable /work so tests, builds and coverage
# files never touch the host. Without a mount, the source baked into the
# image at /opt/codedoc is used instead.
set -eu

src=/src
if [ ! -f "$src/go.mod" ]; then
	src=/opt/codedoc
fi

find /work -mindepth 1 -maxdepth 1 -exec rm -rf {} +
tar -C "$src" \
	--exclude=./codedoc \
	--exclude=./models \
	--exclude='./coverage*.out' \
	-cf - . | tar -C /work -xf -

cd /work
exec "$@"
