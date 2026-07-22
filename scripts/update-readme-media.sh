#!/usr/bin/env bash
set -euo pipefail

video="${1:-}"
full_screenshot="${2:-}"

if [[ -z "$video" || -z "$full_screenshot" ]]; then
  echo 'usage: scripts/update-readme-media.sh <real-journey.webm> <real-full-comparison.png>' >&2
  exit 2
fi
[[ -f "$video" ]] || { echo "missing browser evidence: $video" >&2; exit 2; }
[[ -f "$full_screenshot" ]] || { echo "missing comparison evidence: $full_screenshot" >&2; exit 2; }
command -v ffmpeg >/dev/null || { echo 'ffmpeg is required to render README evidence' >&2; exit 2; }

output=docs/assets/readme
mkdir -p "$output"

# The passing browser journey is short because the candidate already exists.
# Slow it to 9.6 seconds so a README reader can follow failure → comparison.
ffmpeg -y -ss 0.2 -i "$video" -filter_complex \
  '[0:v]setpts=8*PTS,fps=8,scale=1080:-2:flags=lanczos,split[a][b];[a]palettegen=max_colors=128[p];[b][p]paletteuse=dither=bayer:bayer_scale=3' \
  "$output/run-split.gif" >/dev/null 2>&1
ffmpeg -y -ss 1.2 -i "$video" -frames:v 1 -vf 'scale=1280:-2:flags=lanczos' "$output/run-split.png" >/dev/null 2>&1

# Three real evidence beats from the same retained end-to-end investigation.
ffmpeg -y -ss 0.25 -i "$video" -frames:v 1 -vf 'scale=1280:-2:flags=lanczos' "$output/01-failed-run.png" >/dev/null 2>&1
ffmpeg -y -i "$full_screenshot" -frames:v 1 -vf 'crop=900:1060:340:210,scale=900:-2:flags=lanczos' "$output/02-controlled-experiment.png" >/dev/null 2>&1
ffmpeg -y -i "$full_screenshot" -frames:v 1 -vf 'crop=900:480:340:1690,scale=900:-2:flags=lanczos' "$output/03-candidate-resolved.png" >/dev/null 2>&1

size=$(wc -c <"$output/run-split.gif" | tr -d ' ')
((size < 5 * 1024 * 1024)) || { echo "run-split.gif exceeds 5 MB: $size bytes" >&2; exit 1; }
echo "readme-media: rendered real journey evidence (${size} byte GIF)"
