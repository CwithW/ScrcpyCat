param([string]$Serial = "")
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot
$config = Get-Content -Raw "config.json" | ConvertFrom-Json
function Invoke-Adb {
    if ($Serial) { & adb.exe -s $Serial @args } else { & adb.exe @args }
    if ($LASTEXITCODE -ne 0) { throw "adb failed ($LASTEXITCODE)" }
}
function Quote-Shell([string]$value) {
    return "'" + $value.Replace("'", "'\''") + "'"
}
Invoke-Adb get-state
$abi = (Invoke-Adb shell getprop ro.product.cpu.abi).Trim()
if ($abi -notin @("arm64-v8a", "armeabi-v7a", "x86_64", "x86")) { throw "Unsupported ABI: $abi" }
Invoke-Adb shell 'if [ -f /data/local/tmp/scrcpycat-agent.pid ]; then p=$(cat /data/local/tmp/scrcpycat-agent.pid); case "$p" in ""|*[!0-9]*) exit 1;; esac; if [ "$(cat /proc/$p/comm 2>/dev/null)" = scrcpycat-agent ]; then kill "$p"; sleep 1; fi; fi'
Invoke-Adb push "bin/$abi/scrcpycat-agent" "/data/local/tmp/scrcpycat-agent"
Invoke-Adb push "scrcpy-server.jar" "/data/local/tmp/scrcpy-server.jar"
Invoke-Adb shell chmod 755 /data/local/tmp/scrcpycat-agent
$command = "nohup /data/local/tmp/scrcpycat-agent --device-id " + (Quote-Shell $config.device_id) + " --signaling " + (Quote-Shell $config.signaling) + " --enrollment-token " + (Quote-Shell $config.enrollment_token) + " >/data/local/tmp/scrcpycat-agent.log 2>&1 </dev/null &"
Invoke-Adb shell $command
Start-Sleep -Seconds 2
Invoke-Adb shell /data/local/tmp/scrcpycat-agent --health
Write-Host ("Agent ready: " + $config.device_id)
