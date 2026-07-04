# Toolchain (record exact versions used):
#   go 1.26.4, protoc 35.1 (libprotoc), protoc-gen-go v1.36.11, golangci-lint 2.12.2
.PHONY: build test lint vet proto vendor-proto run
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
