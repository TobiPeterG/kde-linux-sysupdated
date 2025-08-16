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

run: kde-linux-sysupdated
	/usr/bin/systemd-socket-activate -l 0.0.0.0:8080 ./kde-linux-sysupdated

