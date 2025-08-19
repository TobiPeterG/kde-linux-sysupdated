# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2023-2025 Harald Sitter <sitter@kde.org>

all: kde-linux-sysupdated

clean:
	rm -rf kde-linux-sysupdated

test:
	go test -v -coverpkg=./... -coverprofile=coverage.cov ./...

kde-linux-sysupdated:
	go install github.com/swaggo/swag/cmd/swag@latest
	go generate
	go build -o kde-linux-sysupdated -v

install: kde-linux-sysupdated
	install -Dm755 kde-linux-sysupdated ${DESTDIR}/usr/lib/kde-linux-sysupdated
	install -Dm644 kde-linux-sysupdated.service ${DESTDIR}/usr/lib/systemd/system/kde-linux-sysupdated.service
	install -Dm644 kde-linux-sysupdated.socket ${DESTDIR}/usr/lib/systemd/system/kde-linux-sysupdated.socket
