VERSION ?= 0.1.0
BINARY = opentracker
PREFIX ?= /usr

.PHONY: build install clean

build:
	go build -ldflags "-X main.version=$(VERSION) -s -w" -o $(BINARY)

install:
	install -Dm755 $(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)

clean:
	rm -f $(BINARY)
