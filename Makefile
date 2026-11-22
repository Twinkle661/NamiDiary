GOFLAGS := -tags=sqlite_fts5
export GOFLAGS
export CGO_ENABLED=1
.PHONY: build test vet check
build:
	go build -buildvcs=false -trimpath -o bin/namidiary ./cmd/namidiary
test:
	go test -race ./...
vet:
	go vet ./...
check: test vet
	test -z "$$(gofmt -l cmd internal)"
