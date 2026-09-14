FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/gadb ./third_party/gadb
RUN go mod download
COPY internal ./internal
COPY agent ./agent
COPY adb-deployer ./adb-deployer
COPY backend ./backend
COPY tools/licenses ./tools/licenses
COPY LICENSE NOTICE /out/licenses/
COPY third_party/gadb/LICENSE /out/licenses/gadb/LICENSE
COPY third_party/scrcpy/LICENSE /out/licenses/scrcpy/LICENSE
RUN CGO_ENABLED=0 go run ./tools/licenses -out /out/licenses/GO_THIRD_PARTY_LICENSES.txt
ARG TARGETOS=linux
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build,sharing=locked CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/scrcpycat-adb-deployer ./adb-deployer/cmd/adb-deployer \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/scrcpycat-agent ./agent/cmd/agent

FROM alpine:3.22
# Alpine supplies native ADB packages for both x86-64 and AArch64.
RUN apk add --no-cache ca-certificates android-tools
COPY --from=build /out/scrcpycat-adb-deployer /usr/local/bin/scrcpycat-adb-deployer
COPY --from=build /out/scrcpycat-agent /usr/local/bin/scrcpycat-agent
COPY --from=build /out/licenses /usr/share/licenses/scrcpycat
COPY third_party/licenses/android-tools /usr/share/licenses/android-tools
# Keep device-mode artifacts in the published image as well as the Linux
# host Agent, so USB deployment does not depend on host bind mounts.
COPY bin/agent /opt/scrcpycat/agent/bin
COPY bin/scrcpy-server /opt/scrcpycat/scrcpy
# The native Linux backend polls the mounted USB device directory. libusb's
# hotplug path can miss container device reattachment after a USB disconnect.
ENV ADB_LIBUSB=0
ENTRYPOINT ["/usr/local/bin/scrcpycat-adb-deployer"]
