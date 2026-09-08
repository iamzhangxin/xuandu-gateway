.PHONY: web generate test race vet build amd64 image
generate:
	go generate ./internal/app
test:
	go test ./...
race:
	go test -race ./...
vet:
	go vet ./...
web:
	cd web && npm ci && npm run build
build: web
	go build -trimpath -o bin/xuandu ./cmd
amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o bin/xuandu-linux-amd64 ./cmd
image:
	docker build --platform linux/amd64 -t xuandu:local .
