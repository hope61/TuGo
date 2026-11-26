PREFIX       ?= /opt/tugo
# the service runs as whoever invoked sudo, so it uses their git credentials
SERVICE_USER ?= $(or $(SUDO_USER),$(USER))
UNIT         := /etc/systemd/system/tugo.service
# run git as the sudo user, git refuses to read a repo owned by someone else when run as root
VERSION      ?= $(shell $(if $(SUDO_USER),sudo -u $(SUDO_USER)) git describe --tags --long --always --dirty 2>/dev/null || echo dev)

.PHONY: build install service uninstall test

build:
	go build -ldflags "-X main.version=$(VERSION)" -o tugo ./cmd/tugo/

install: build
	mkdir -p $(PREFIX)
	install -m 755 tugo $(PREFIX)/tugo
	ln -sf $(PREFIX)/tugo /usr/local/bin/tugo
	@if [ ! -f $(PREFIX)/config.yaml ]; then \
		if [ -f config.yaml ]; then install -m 644 config.yaml $(PREFIX)/config.yaml; \
		else install -m 644 example-config.yaml $(PREFIX)/config.yaml; fi; \
	fi
	chown -R $(SERVICE_USER) $(PREFIX)

service: install
	printf '%s\n' \
		'[Unit]' \
		'Description=Tugo auto-pull' \
		'After=network-online.target' \
		'Wants=network-online.target' \
		'' \
		'[Service]' \
		'User=$(SERVICE_USER)' \
		'ExecStart=$(PREFIX)/tugo' \
		'Restart=always' \
		'RestartSec=10' \
		'' \
		'[Install]' \
		'WantedBy=multi-user.target' > $(UNIT)
	systemctl daemon-reload
	systemctl enable --now tugo
	systemctl restart tugo

test:
	go test ./...

uninstall:
	-systemctl disable --now tugo
	rm -f $(UNIT)
	systemctl daemon-reload
	rm -rf $(PREFIX)
	rm -f /usr/local/bin/tugo
