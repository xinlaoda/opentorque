PREFIX ?= /usr/local
GOFLAGS ?= -trimpath
VERSION ?= 0.1.0
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

DAEMONS := pbs_server pbs_mom pbs_sched
CLI_TOOLS := qsub qstat qdel qhold qrls pbsnodes qmgr

.PHONY: all server mom sched cli clean install test fmt vet

all: server mom sched cli

server:
	go build $(GOFLAGS) $(LDFLAGS) -o bin/pbs_server ./cmd/pbs_server

mom:
	go build $(GOFLAGS) $(LDFLAGS) -o bin/pbs_mom ./cmd/pbs_mom

sched:
	go build $(GOFLAGS) $(LDFLAGS) -o bin/pbs_sched ./cmd/pbs_sched

cli:
	@for tool in $(CLI_TOOLS); do \
		go build $(GOFLAGS) $(LDFLAGS) -o bin/$$tool ./cmd/$$tool; \
	done

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -rf bin/

install: all
	install -d $(PREFIX)/sbin $(PREFIX)/bin
	@for d in $(DAEMONS); do \
		install -m 755 bin/$$d $(PREFIX)/sbin/$$d; \
	done
	@for t in $(CLI_TOOLS); do \
		install -m 755 bin/$$t $(PREFIX)/bin/$$t; \
	done

uninstall:
	@for d in $(DAEMONS); do rm -f $(PREFIX)/sbin/$$d; done
	@for t in $(CLI_TOOLS); do rm -f $(PREFIX)/bin/$$t; done
