.DEFAULT_GOAL := test

BIN_DIR ?= bin
SCRCPY_VERSION ?= 4.1
ANDROID_API ?= 21
ANDROID_NDK_HOME ?=
VERSION ?= 0.1.0-dev
REVISION ?= unknown
LDFLAGS := -s -w -X github.com/cwithw/ScrcpyCat/internal/buildinfo.Version=$(VERSION) -X github.com/cwithw/ScrcpyCat/internal/buildinfo.Revision=$(REVISION)
DOCKER_PROXY_ARGS ?= --build-arg HTTP_PROXY="$(http_proxy)" --build-arg HTTPS_PROXY="$(https_proxy)" --build-arg ALL_PROXY="$(all_proxy)" --build-arg NO_PROXY="$(no_proxy)"
ANDROID_TOOLCHAIN := $(ANDROID_NDK_HOME)/toolchains/llvm/prebuilt/linux-x86_64/bin

.PHONY: test test-frontend fmt build-frontend build-controlplane build-adb-deployer build-host-agent build-agent build-agent-docker build-scrcpy-server release clean

test:
	go test ./...

test-frontend:
	npm --prefix "frontend/web-app" test

fmt:
	gofmt -w backend agent adb-deployer internal tools

build-frontend:
	npm --prefix "frontend/web-app" ci --no-audit --no-fund
	npm --prefix "frontend/web-app" run build

build-controlplane: build-frontend
	mkdir -p "$(BIN_DIR)"
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o "$(BIN_DIR)/scrcpycat-controlplane" ./backend/cmd/controlplane

build-adb-deployer:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/scrcpycat-adb-deployer ./adb-deployer/cmd/adb-deployer

build-host-agent:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/scrcpycat-host-agent ./agent/cmd/agent

build-agent:
	@test -n "$(ANDROID_NDK_HOME)" || (echo "ANDROID_NDK_HOME must point to an Android NDK (for example r27c)" && exit 2)
	@test -x "$(ANDROID_TOOLCHAIN)/aarch64-linux-android$(ANDROID_API)-clang" || (echo "Android NDK LLVM toolchain not found under $(ANDROID_NDK_HOME)" && exit 2)
	install -d -m 0755 "$(BIN_DIR)/agent" "$(BIN_DIR)/agent/arm64-v8a" "$(BIN_DIR)/agent/armeabi-v7a" "$(BIN_DIR)/agent/x86_64" "$(BIN_DIR)/agent/x86"
	CC=$(ANDROID_TOOLCHAIN)/aarch64-linux-android$(ANDROID_API)-clang CGO_ENABLED=1 GOOS=android GOARCH=arm64 go build -trimpath -ldflags="-s -w -checklinkname=0" -o $(BIN_DIR)/agent/arm64-v8a/scrcpycat-agent ./agent/cmd/agent
	CC=$(ANDROID_TOOLCHAIN)/armv7a-linux-androideabi$(ANDROID_API)-clang CGO_ENABLED=1 GOOS=android GOARCH=arm GOARM=7 go build -trimpath -ldflags="-s -w -checklinkname=0" -o $(BIN_DIR)/agent/armeabi-v7a/scrcpycat-agent ./agent/cmd/agent
	CC=$(ANDROID_TOOLCHAIN)/x86_64-linux-android$(ANDROID_API)-clang CGO_ENABLED=1 GOOS=android GOARCH=amd64 go build -trimpath -ldflags="-s -w -checklinkname=0" -o $(BIN_DIR)/agent/x86_64/scrcpycat-agent ./agent/cmd/agent
	CC=$(ANDROID_TOOLCHAIN)/i686-linux-android$(ANDROID_API)-clang CGO_ENABLED=1 GOOS=android GOARCH=386 go build -trimpath -ldflags="-s -w -checklinkname=0" -o $(BIN_DIR)/agent/x86/scrcpycat-agent ./agent/cmd/agent

build-agent-docker:
	install -d -m 0755 "$(BIN_DIR)/agent"
	docker build $(DOCKER_PROXY_ARGS) --output "type=local,dest=$(BIN_DIR)/agent" -f "agent/Dockerfile.build" "."

# Bind-mounted artifacts must be readable by the non-root control plane.
build-scrcpy-server:
	install -d -m 0755 "$(BIN_DIR)/scrcpy-server"
	docker build $(DOCKER_PROXY_ARGS) --output type=local,dest=$(BIN_DIR)/scrcpy-server -f third_party/scrcpy/Dockerfile.server .

release: build-frontend build-agent-docker build-scrcpy-server
	bash "tools/release.sh" "$(VERSION)" "$(REVISION)"

clean:
	rm -rf "$(BIN_DIR)"
