.PHONY: run demo test vet build-linux clean

run:
	LEDGER_ADMIN_USER=dev LEDGER_ADMIN_PASSWORD=devdevdev LEDGER_ADDR=127.0.0.1:18080 go run .

demo:
	rm -rf demo-data
	mkdir -p demo-data
	LEDGER_DB=demo-data/ledger.db go run ./cmd/demo
	LEDGER_DATA=demo-data LEDGER_ADDR=127.0.0.1:18080 go run .

test:
	go vet ./...
	go test ./...

vet:
	go vet ./...

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/ledger-linux-amd64 .

clean:
	rm -rf dist demo.db demo.db-wal demo.db-shm demo-data
