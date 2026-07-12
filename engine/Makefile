# Toolchain (record exact versions used):
#   go 1.26.4, protoc 35.1 (libprotoc), protoc-gen-go v1.36.11, golangci-lint 2.12.2
#
# .github/workflows/release.yml builds release-windows/release-macos on tag push;
# .github/workflows/ci.yml runs `make -C engine gen-ts-check` as part of the PR/push
# gate, alongside go test/vet/lint and the UI suite. The `gen-ts`/`gen-ts-check`
# targets remain usable locally to regenerate or verify before pushing.
#
# VERSION feeds -ldflags -X into internal/buildinfo.Version for release builds
# (see release-windows/release-macos below). Falls back to "dev" when git
# metadata is unavailable (e.g. a source tarball with no .git directory).
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test lint vet proto vendor-proto run gen-ts gen-ts-check release-windows release-macos embed-ui windows-icon
build:
	go build ./...
test:
	go test -race ./...
vet:
	go vet ./...
lint:
	golangci-lint run
proto:
	./scripts/gen_proto.sh
vendor-proto:
	python3 scripts/vendor_protos.py
run:
	go run ./cmd/etape
gen-ts:
	go tool tygo generate
gen-ts-check: gen-ts
	git ls-files --error-unmatch -- ../ui/src/gen/wsmsg.ts > /dev/null 2>&1 || \
	  (echo "wsmsg contract drift: ui/src/gen/wsmsg.ts is not tracked by git" && exit 1)
	git diff --exit-code -- ../ui/src/gen/wsmsg.ts || \
	  (echo "wsmsg contract drift: ui/src/gen/wsmsg.ts is stale — run 'make gen-ts' and commit" && exit 1)

# release-windows/release-macos build the real UI, bake it into the binary via
# the embed_ui build tag (internal/webui/embed.go), and cross-compile with
# CGO disabled (no cgo toolchain needed on the build machine or target). Both
# targets are self-contained: the ui/dist copy step means neither `make build`
# nor `go test` picks up embedded UI content, since embed_ui is only set here.
#
# -H=windowsgui on the Windows target links the binary against the Windows
# subsystem instead of the console subsystem, which is what actually
# suppresses the console window for the tray build (run_tray.go, tags=tray) --
# the tray build tag alone only swaps main()'s behavior, it doesn't touch the
# PE header.
# UI build + embed copy, shared by both release targets (built once per `make` run).
embed-ui:
	cd ../ui && npm run build
	rm -rf internal/webui/dist && cp -r ../ui/dist internal/webui/dist

# Generates the Windows PE icon resource from assets/etape.ico +
# versioninfo.json. The _windows_amd64 filename suffix is Go's own build-file
# naming convention, so `go build` links it into GOOS=windows/GOARCH=amd64
# builds only -- release-macos never sees it, and it's gitignored (generated,
# not committed).
#
# -arm=false is required despite targeting amd64: goversioninfo's -arm flag
# also defaults to true, and its arch-selection logic checks IsARM before
# Is64Bit -- so -64 alone silently produces an arm64 resource instead of
# amd64 (verified 2026-07-10: without -arm=false the .syso's COFF machine
# field is 0xaa64/arm64, not 0x8664/amd64).
windows-icon:
	go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.7.0 \
	  -64 -arm=false -icon cmd/etape/assets/etape.ico \
	  -o cmd/etape/etape_windows_amd64.syso \
	  cmd/etape/versioninfo.json

release-windows: embed-ui windows-icon
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags "embed_ui tray" \
	  -ldflags "-s -w -H=windowsgui -X github.com/earlisreal/eTape/engine/internal/buildinfo.Version=$(VERSION)" \
	  -o ../dist/etape-windows-amd64.exe ./cmd/etape
release-macos: embed-ui
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -tags embed_ui \
	  -ldflags "-s -w -X github.com/earlisreal/eTape/engine/internal/buildinfo.Version=$(VERSION)" \
	  -o ../dist/etape-darwin-arm64 ./cmd/etape
