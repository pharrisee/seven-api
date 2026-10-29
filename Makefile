GO ?= go
BINARY ?= server
ADDR ?= 127.0.0.1:8080

.PHONY: build run test race vet lint vuln sec check clean

build:
	$(GO) build -trimpath -o $(BINARY) .

run:
	$(GO) run . -addr $(ADDR)

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint: vet
	staticcheck ./...

vuln:
	govulncheck ./...

sec: vuln
	gosec ./...

check: race vet lint

clean:
	rm -f $(BINARY)
	rm -rf tmp
