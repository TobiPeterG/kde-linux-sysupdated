# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2023-2025 Harald Sitter <sitter@kde.org>

all: kde-linux-sysupdated

clean:
	rm -rf kde-linux-sysupdated
	rm -rf kde-linux-sysupdated-redirector

test:
	go test -v -coverpkg=./... -coverprofile=coverage.cov ./...

kde-linux-sysupdated:
	go build -o kde-linux-sysupdated -v

kde-linux-sysupdated-redirector:
	go build -o kde-linux-sysupdated-redirector -v ./redirector

install: kde-linux-sysupdated kde-linux-sysupdated-redirector
	install -Dm755 kde-linux-sysupdated ${DESTDIR}/usr/lib/kde-linux-sysupdated
	install -Dm755 kde-linux-sysupdated-redirector ${DESTDIR}/usr/lib/kde-linux-sysupdated-redirector
	install -Dm644 kde-linux-sysupdated.service ${DESTDIR}/usr/lib/systemd/system/kde-linux-sysupdated.service
	install -Dm644 kde-linux-sysupdated.socket ${DESTDIR}/usr/lib/systemd/system/kde-linux-sysupdated.socket

run:
	make clean
	make
	DESTDIR=~/kde/ make install
	run0 systemctl restart systemd-sysext
	run0 systemctl daemon-reload
	run0 systemctl stop kde-linux-sysupdated.service
	run0 systemctl restart kde-linux-sysupdated.socket
	journalctl -xef _SYSTEMD_UNIT=kde-linux-sysupdated.service
