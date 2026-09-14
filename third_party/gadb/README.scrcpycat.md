# Local gadb changes

Source: `github.com/electricbubble/gadb` v0.1.0
(`e2a2fccc8c86406b074c6226dbfc23ea32bee51b`), MIT licensed.

This small source copy keeps the upstream device enumeration and Sync file
transfers. `context.go` adds cancellable connections, explicit serial binding,
and access to full-duplex device services. Existing transport construction uses
these connections; failed transport selection closes its socket. Shell v2 and
application lifecycle policy live in ScrcpyCat's `internal/adb` package.

The library and extensions use only Go's standard library, with no cgo or USB
driver replacement. The official ADB server remains the USB owner.
