#!/system/bin/sh
MODDIR=$(dirname "$0")
while [ "$(getprop sys.boot_completed)" != "1" ]; do sleep 2; done
. "$MODDIR/config.env"
ABI=$(getprop ro.product.cpu.abi)
STATE=/data/adb/scrcpycat
mkdir -p "$STATE"
chmod 700 "$STATE"
while [ ! -f "$MODDIR/disable" ] && [ ! -f "$MODDIR/remove" ]; do
  "$MODDIR/bin/$ABI/scrcpycat-agent" --device-id "$SCRCPYCAT_DEVICE_ID" --signaling "$SCRCPYCAT_SIGNALING" --enrollment-token "$SCRCPYCAT_ENROLLMENT_TOKEN" --scrcpy-jar "$MODDIR/scrcpy-server.jar" --identity-file "$STATE/identity.json" --pid-file "$STATE/agent.pid" >>"$STATE/agent.log" 2>&1
  sleep 5
done
