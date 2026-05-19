VERSION ?= 1.3.0
BINARY = opentracker
PREFIX ?= /usr

.PHONY: build install clean test coverage

build:
	go build -ldflags "-X main.version=$(VERSION) -s -w" -o $(BINARY)

install:
	install -Dm755 $(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)

clean:
	rm -f $(BINARY)

test:
	go test ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
