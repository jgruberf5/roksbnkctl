#!/usr/bin/env bash
# Record this demo to an MP4 with the shared recorder (../lib/record-demo.sh):
# a headless X terminal runs the demo hands-off, ffmpeg captures it, and
# post_10x.py 10x's the long windows and holds each roksbnkctl command 5s.
#
# Fill in .env first (see .env.example), then:  ./record.sh
# Output: ./demo-video/blueprint-workflows-ci-demo.mp4
set -uo pipefail
HERE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
exec env DEMO_SCRIPT="$HERE/blueprint-workflows-ci-demo.sh" "$HERE/../lib/record-demo.sh" "$@"
