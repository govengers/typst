set shell := ["bash", "-euo", "pipefail", "-c"]

# govengers/typst, from the library template of govengers/standards (templates/library).
# A library pins its tools here with go run, not with tool directives in go.mod (TCH-002).

gotestsum := "go run gotest.tools/gotestsum@v1.13.0"
govulncheck := "go run golang.org/x/vuln/cmd/govulncheck@v1.9.0"
coverage_threshold := "80"
coverage_excluded := "/examples/|/test/"
e2e_inputs := "test/e2e"

# The typst release the end-to-end suite runs against, with the SHA-256 of each release archive
typst_version := "0.15.1"
typst_binary := justfile_directory() / "sandbox/typst" / typst_version / "typst"

default:
    @just --list

# Vet and lint with the govengers baseline (.golangci.yml, copied from govengers/standards, LIB-005 checks enabled)
lint:
    go vet ./...
    go vet -tags e2e ./test/e2e/...
    golangci-lint run ./...

# Unit tests of changed and affected packages; the Go test cache replays the rest (TST-014). Fails below the coverage threshold (TST-010)
test *flags:
    mkdir -p sandbox
    {{gotestsum}} --format testname --jsonfile sandbox/unit.json -- -race -coverprofile=sandbox/unit.raw.cover {{flags}} ./...
    grep -v -E '{{coverage_excluded}}' sandbox/unit.raw.cover > sandbox/unit.cover
    go tool cover -func=sandbox/unit.cover > sandbox/unit.func
    awk -v threshold={{coverage_threshold}} '/^total:/ { found = 1; sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < threshold) { print "coverage is below " threshold "%"; exit 1 } } END { if (!found) { print "no coverage total"; exit 1 } }' sandbox/unit.func

# Every unit test, uncached and shuffled: the nightly run and before a release (TST-014, TST-005)
test-all: (test "-count=1" "-shuffle=on")

# Install the pinned typst release into sandbox/typst for the end-to-end suite, checksum verified
install-typst:
    #!/usr/bin/env bash
    set -euo pipefail
    target="{{typst_binary}}"
    if [ -x "$target" ]; then exit 0; fi
    case "$(uname -s)-$(uname -m)" in
        Linux-x86_64) asset=typst-x86_64-unknown-linux-musl; sha256=a6d077d0a95eed5a2eba715b2dae06be954f624ccbf85758a03f389ded33118c ;;
        Linux-aarch64 | Linux-arm64) asset=typst-aarch64-unknown-linux-musl; sha256=5aa8d74a3d906e60ea12a66ac2f37f8eef1b14cbad7182a745e393a10c23dcee ;;
        Darwin-arm64) asset=typst-aarch64-apple-darwin; sha256=48f62ed034aa3a7978309579ac6ca00045e2ef0da73114e8af27cfd8e74dc05a ;;
        Darwin-x86_64) asset=typst-x86_64-apple-darwin; sha256=7f9fdd9584866245de9a79e0add8f9236fae6f40a8a45e2c4771ccc14db4e0fa ;;
        *) echo "no typst {{typst_version}} release for $(uname -s)-$(uname -m)"; exit 1 ;;
    esac
    download=$(mktemp -d)
    trap 'rm -rf "$download"' EXIT
    curl -fsSL --retry 3 -o "$download/typst.tar.xz" "https://github.com/typst/typst/releases/download/v{{typst_version}}/$asset.tar.xz"
    echo "$sha256  $download/typst.tar.xz" | shasum -a 256 -c - > /dev/null
    tar -xJf "$download/typst.tar.xz" -C "$download"
    mkdir -p "$(dirname "$target")"
    mv "$download/$asset/typst" "$target"
    "$target" --version

# Reproducible compiled suite: its hash says whether the suite has anything new to test (TST-015)
e2e-build:
    mkdir -p sandbox/e2e-build
    go test -c -race -tags e2e -trimpath -buildvcs=false -o sandbox/e2e-build/ ./test/e2e/...

# The end-to-end suite against the real typst release, always (TST-011)
test-e2e-all: install-typst e2e-build
    mkdir -p sandbox
    TYPST_BINARY="{{typst_binary}}" {{gotestsum}} --format testname --jsonfile sandbox/e2e.json -- -race -tags e2e -trimpath -buildvcs=false -count=1 ./test/e2e/...

# The end-to-end suite, skipped when suite, inputs and typst release equal a green run (TST-015); keeps sandbox/e2e.json for CI badges
test-e2e: e2e-build
    #!/usr/bin/env bash
    set -euo pipefail
    key=$( (find sandbox/e2e-build {{e2e_inputs}} -type f -print0 | sort -z | xargs -0 shasum -a 256; echo "typst {{typst_version}}") | shasum -a 256 | cut -c1-32)
    passed="sandbox/e2e-passed/$key"
    if [ -f "$passed/e2e.json" ]; then
        echo "e2e skipped: suite and inputs equal the green run $key"
        cp "$passed/e2e.json" sandbox/e2e.json
        exit 0
    fi
    "{{just_executable()}}" test-e2e-all
    mkdir -p "$passed"
    cp sandbox/e2e.json "$passed/e2e.json"

# Scan for known vulnerabilities in reachable code (TCH-008)
vulncheck:
    {{govulncheck}} ./...

# Everything CI runs on a change
check: lint test test-e2e vulncheck
