#!/bin/sh
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
: "${TMPDIR:?TMPDIR must be set}"
export GOCACHE="$TMPDIR/boxpilot-go-cache" GOMODCACHE="$TMPDIR/boxpilot-go-mod" GOPATH="$TMPDIR/boxpilot-go-path" CGO_ENABLED=0
TOOLS="$TMPDIR/boxpilot-proto-tools"
mkdir -p "$TOOLS"
# Install in a separate module so generation never edits the application go.mod.
if [ ! -f "$TOOLS/go.mod" ]; then
  printf 'module boxpilot-proto-tools\n\ngo 1.27.3\n' > "$TOOLS/go.mod"
fi
(cd "$TOOLS" && GOBIN="$TOOLS" go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12)
(cd "$TOOLS" && GOBIN="$TOOLS" go install connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.21.1)
export PATH="$TOOLS:$PATH" XDG_CACHE_HOME="$TMPDIR/boxpilot-tool-cache" BUF_CACHE_DIR="$TMPDIR/boxpilot-buf-cache"
go run github.com/bufbuild/buf/cmd/buf@v1.75.0 generate
