#!/bin/bash
# Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
# SPDX-License-Identifier: Apache-2.0
#
# protolift idle stop. Runs every 5 minutes as root.
#
# Activity is real use only:
#  1. bytes moved by sshd: the port tunnel and file sync both pass through
#     it, while keepalives stay far below the threshold;
#  2. an open terminal (protolift ssh / logs).
# A connected-but-quiet tunnel is NOT activity, so a box left overnight
# stops. The box also always stops after max_hours, and a CloudWatch alarm
# outside the box stops it after an hour of low CPU.
set -u
STATE=/var/lib/protolift
CONF=/etc/protolift/idle.conf
mkdir -p "$STATE"

# Read numbers only; the config is never executed.
num() { grep -E "^$1=[0-9]+$" "$CONF" 2>/dev/null | tail -1 | cut -d= -f2; }
IDLE_MINUTES=$(num IDLE_MINUTES); IDLE_MINUTES=${IDLE_MINUTES:-30}
MAX_HOURS=$(num MAX_HOURS); MAX_HOURS=${MAX_HOURS:-12}
THRESHOLD_BYTES=65536
STEP_MINUTES=5

# Remember the highest memory use, for `protolift status` size advice.
used_kb=$(awk '/^MemTotal:/{t=$2} /^MemAvailable:/{a=$2} END{print t-a}' /proc/meminfo)
peak_kb=$(cat "$STATE/mem-peak-kb" 2>/dev/null || echo 0)
if [ "$used_kb" -gt "$peak_kb" ]; then echo "$used_kb" > "$STATE/mem-peak-kb"; fi
chmod 644 "$STATE/mem-peak-kb" 2>/dev/null

up_minutes=$(( $(cut -d. -f1 /proc/uptime) / 60 ))
if [ "$up_minutes" -ge $(( MAX_HOURS * 60 )) ]; then
  logger -t protolift "running for ${MAX_HOURS}h (max_hours): stopping"
  shutdown -h now
  exit 0
fi

bytes=0
for pid in $(pgrep -x 'sshd|sshd-session'); do
  n=$(awk '/^(rchar|wchar):/{s+=$2} END{print s+0}' "/proc/$pid/io" 2>/dev/null || echo 0)
  bytes=$(( bytes + n ))
done
prev=$(cat "$STATE/ssh-bytes" 2>/dev/null || echo 0)
echo "$bytes" > "$STATE/ssh-bytes"
moved=$(( bytes - prev ))

active=0
if [ "$moved" -gt "$THRESHOLD_BYTES" ]; then active=1; fi
if who | grep -q 'pts/'; then active=1; fi

if [ "$active" -eq 1 ]; then
  echo 0 > "$STATE/idle-minutes"
  exit 0
fi
idle=$(( $(cat "$STATE/idle-minutes" 2>/dev/null || echo 0) + STEP_MINUTES ))
echo "$idle" > "$STATE/idle-minutes"
if [ "$idle" -ge "$IDLE_MINUTES" ]; then
  logger -t protolift "idle for ${idle} minutes: stopping"
  echo 0 > "$STATE/idle-minutes"
  shutdown -h now
fi
