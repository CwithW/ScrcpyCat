#!/bin/sh
set -eu
cd "$(dirname "$0")"
. ./config.env
SCRCPYCAT_SERIAL=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --serial) SCRCPYCAT_SERIAL="$2"; shift 2 ;;
    --device-id) SCRCPYCAT_DEVICE_ID="$2"; shift 2 ;;
    --signaling) SCRCPYCAT_SIGNALING="$2"; shift 2 ;;
    *) echo "Unknown option: $1" >&2; exit 2 ;;
  esac
done
adb_cmd() {
  if [ -n "$SCRCPYCAT_SERIAL" ]; then adb -s "$SCRCPYCAT_SERIAL" "$@"; else adb "$@"; fi
}
quote() { printf "'"; printf "%s" "$1" | sed "s/'/'\\\\''/g"; printf "'"; }
adb_cmd get-state >/dev/null
ABI=$(adb_cmd shell getprop ro.product.cpu.abi | tr -d '\r\n')
case "$ABI" in arm64-v8a|armeabi-v7a|x86_64|x86) ;; *) echo "Unsupported ABI: $ABI" >&2; exit 1;; esac
adb_cmd shell 'if [ -f /data/local/tmp/scrcpycat-agent.pid ]; then p=$(cat /data/local/tmp/scrcpycat-agent.pid); case "$p" in ""|*[!0-9]*) exit 1;; esac; if [ "$(cat /proc/$p/comm 2>/dev/null)" = scrcpycat-agent ]; then kill "$p"; sleep 1; fi; fi'
adb_cmd push "bin/$ABI/scrcpycat-agent" /data/local/tmp/scrcpycat-agent
adb_cmd push scrcpy-server.jar /data/local/tmp/scrcpy-server.jar
adb_cmd shell chmod 755 /data/local/tmp/scrcpycat-agent
COMMAND="nohup /data/local/tmp/scrcpycat-agent --device-id $(quote "$SCRCPYCAT_DEVICE_ID") --signaling $(quote "$SCRCPYCAT_SIGNALING") --enrollment-token $(quote "$SCRCPYCAT_ENROLLMENT_TOKEN") >/data/local/tmp/scrcpycat-agent.log 2>&1 </dev/null &"
adb_cmd shell "$COMMAND"
sleep 2
adb_cmd shell /data/local/tmp/scrcpycat-agent --health
printf "Agent ready: %s\n" "$SCRCPYCAT_DEVICE_ID"
