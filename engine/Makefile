# Toolchain (record exact versions used):
#   go 1.26.4, protoc 35.1 (libprotoc), protoc-gen-go v1.36.11, golangci-lint 2.12.2
#
# No CI workflow file exists in this repo yet (checked .github/workflows/ during
# Plan 6 Task 4 — absent); `gen-ts-check` below is the authoritative drift gate
# and must pass before merge.
.PHONY: build test lint vet proto vendor-proto run gen-ts gen-ts-check
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
