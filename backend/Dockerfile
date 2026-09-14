FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend
WORKDIR /src/frontend/web-app
COPY frontend/web-app/package*.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY frontend/web-app ./
COPY frontend/LICENSE /src/frontend/LICENSE
COPY LICENSE NOTICE /src/
COPY third_party/licenses /src/third_party/licenses
COPY tools/frontend-licenses.mjs /src/tools/frontend-licenses.mjs
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/gadb ./third_party/gadb
RUN go mod download
COPY internal ./internal
COPY backend ./backend
COPY agent ./agent
COPY adb-deployer ./adb-deployer
COPY tools/licenses ./tools/licenses
COPY LICENSE NOTICE /out/licenses/
COPY frontend/LICENSE /out/licenses/frontend/LICENSE
COPY third_party/scrcpy/LICENSE /out/licenses/scrcpy/LICENSE
COPY third_party/gadb/LICENSE /out/licenses/gadb/LICENSE
COPY --from=frontend /src/backend/internal/webui/dist ./backend/internal/webui/dist
RUN mkdir -p /out/assets && CGO_ENABLED=0 go run ./tools/licenses -out /out/licenses/GO_THIRD_PARTY_LICENSES.txt
COPY --from=frontend /src/backend/internal/webui/dist/THIRD_PARTY_LICENSES.txt /out/licenses/FRONTEND_THIRD_PARTY_LICENSES.txt
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=0.1.0-dev
ARG REVISION=unknown
RUN --mount=type=cache,target=/root/.cache/go-build,sharing=locked CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X github.com/cwithw/ScrcpyCat/internal/buildinfo.Version=$VERSION -X github.com/cwithw/ScrcpyCat/internal/buildinfo.Revision=$REVISION" -o /out/scrcpycat-controlplane ./backend/cmd/controlplane

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/scrcpycat-controlplane /usr/local/bin/scrcpycat-controlplane
COPY --from=build --chown=65532:65532 /out/assets /var/lib/scrcpycat/assets
COPY --from=build /out/licenses /usr/share/licenses/scrcpycat
EXPOSE 8443
ENTRYPOINT ["/usr/local/bin/scrcpycat-controlplane"]
