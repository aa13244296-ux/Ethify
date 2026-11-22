.PHONY: all relay client test clean

all: relay client

relay:
	@echo "==> Building Ethify Relay (Linux amd64)..."
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/ethify-relay ./cmd/ethify-relay

relay-arm64:
	@echo "==> Building Ethify Relay (Linux arm64)..."
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/ethify-relay-arm64 ./cmd/ethify-relay

client:
	@echo "==> Building Ethifi Client (Windows amd64)..."
	powershell -ExecutionPolicy Bypass -File scripts/build-windows.ps1

client-mac:
	@echo "==> Building Ethify Client (macOS arm64)..."
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/ethify-client-darwin-arm64 ./cmd/ethify-client

client-linux:
	@echo "==> Building Ethify Client (Linux / Steam Deck amd64)..."
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/ethify-client-linux-amd64 ./cmd/ethify-client

test:
	@echo "==> Running All Unit Tests..."
	go test -v ./...

clean:
	rm -rf bin/ethify-relay bin/ethify-relay-arm64 bin/ethify-client.exe
