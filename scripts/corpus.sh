#!/usr/bin/env sh
# Reproducible corpus workflows. Run from any directory; paths below are
# relative to the repository root. Setup is the only dependency-install step.
set -eu

corpus_repo=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
cd "$corpus_repo"
corpus_venv=${CORPUS_VENV:-"$corpus_repo/.venv"}
corpus_python="$corpus_venv/bin/python"
corpus_data=${CORPUS_DIR:-.corpus}
corpus_command=${1:-help}
if [ "$#" -gt 0 ]; then shift; fi

help() {
    cat <<'EOF'
Usage: ./scripts/corpus.sh COMMAND [harness options]

  setup       Install hash-locked dependencies and build the Go tools
  test        Run Go checks, parser tests, and offline region expectations
  fetch       Download and normalize the pinned YAML examples
  sample      Survey 50 examples with a 6-second deadline per template
  full        Survey every example with the original 6-second deadline
  resume      Resume the same full run without rebuilding its validator
  diagnose    Reproduce the four-template, 13-resource investigation
  schemas     Regenerate all schemas from the checked-in snapshot into scratch
  regenerate  Rebuild the checked-in smoke fixtures and expectations
  setup-flip  Install the optional, hash-locked CFN Flip comparison tools
  flip        Fetch examples with both parsers and report normalization differences
  compare     Compare two report directories with identical inputs and budgets

Start with setup, then test. Download examples with fetch before sample/full/
diagnose. Read .corpus-*-report/report.md after a survey.

Options after the command are forwarded to corpus.py (or the generator for
schemas). Repeat the same full options when resuming, including --report.
Paths are relative to the repository root, even when invoked elsewhere.

Environment: CORPUS_PYTHON selects the setup interpreter (default python3),
CORPUS_VENV selects the virtual environment, CORPUS_DIR selects downloaded data.
EOF
}

die() {
    printf '%s\n' "$*" >&2
    exit 2
}

require_environment() {
    [ -x "$corpus_python" ] || die 'Run ./scripts/corpus.sh setup first.'
    "$corpus_python" corpus/check_environment.py corpus/requirements.txt
}

build_tools() {
    command -v go >/dev/null 2>&1 || die 'Go 1.26 or newer is required.'
    # Exclude checkout paths and Git metadata from the binaries. Rebuilding
    # unchanged Go sources with the same toolchain keeps their hashes stable.
    go build -trimpath -buildvcs=false -o bin/cfn-cue ./cmd/cfn-cue
    go build -trimpath -buildvcs=false -o bin/cfn-cue-validate ./cmd/cfn-cue-validate
}

setup() {
    corpus_bootstrap=${CORPUS_PYTHON:-python3}
    command -v "$corpus_bootstrap" >/dev/null 2>&1 || die 'Python 3.12 or newer is required.'
    "$corpus_bootstrap" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 12) else "Python 3.12 or newer is required.")'
    command -v go >/dev/null 2>&1 || die 'Go 1.26 or newer is required.'
    if [ -e "$corpus_venv" ] && [ ! -f "$corpus_venv/pyvenv.cfg" ]; then
        die "Refusing to replace a directory that is not a virtual environment: $corpus_venv"
    fi
    printf '%s\n' 'Preparing the Python environment and locked dependencies...'
    "$corpus_bootstrap" -m venv "$corpus_venv"
    "$corpus_python" -m pip install --disable-pip-version-check --require-hashes -r "$1"
    "$corpus_python" corpus/check_environment.py "$1"
    go mod download
    build_tools
    printf '%s\n' 'Setup complete. Run ./scripts/corpus.sh test next.'
}

require_downloads() {
    corpus_check_data=$corpus_data
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --corpus)
                shift
                [ "$#" -gt 0 ] || die '--corpus requires a directory.'
                corpus_check_data=$1
                ;;
            --corpus=*) corpus_check_data=${1#--corpus=} ;;
        esac
        shift
    done
    [ -f "$corpus_check_data/index.json" ] || die 'Run ./scripts/corpus.sh fetch first, or select a downloaded corpus with CORPUS_DIR or --corpus.'
}

full() {
    "$corpus_python" corpus/corpus.py run --corpus "$corpus_data" \
        --schemas generated --validator bin/cfn-cue-validate \
        --regions us-east-1 --limit 0 --timeout 6 --memory 2GiB \
        --resources none --report .corpus-full-report "$@"
}

case "$corpus_command" in
    help|-h|--help)
        help
        ;;
    compare)
        require_environment
        "$corpus_python" corpus/compare.py "$@"
        ;;
    setup|setup-flip)
        [ "$#" -eq 0 ] || die 'Setup takes no options; use CORPUS_PYTHON or CORPUS_VENV to change the environment.'
        if [ "$corpus_command" = setup-flip ]; then
            setup corpus/requirements-flip.txt
        else
            setup corpus/requirements.txt
        fi
        ;;
    test)
        require_environment
        build_tools
        printf '%s\n' 'Checking Go formatting, unit tests, and vet...'
        [ -z "$(gofmt -l cmd internal)" ] || die 'Go formatting differs; run gofmt on cmd/ and internal/.'
        # Budget the complete regression suite separately from the explicitly
        # bounded regional performance measurements. Linux CI is slower than
        # the local arm64 runner, and the dual-package coverage adds CUE builds.
        go test ./... -timeout 3m
        go vet ./...
        printf '%s\n' 'Checking the pinned S3/IAM schema integration...'
        CFN_CUE_INTEGRATION=1 go test ./internal/generate -run '^TestCurrentResources$' -v -timeout 3m
        printf '%s\n' 'Checking IAM Role AWS::NoValue removals and the corpus against the complete regional templates...'
        CFN_CUE_INTEGRATION=1 go test ./internal/validate -run '^(TestIAMRoleNoValue|TestCorpusRegionalTemplates)$' -v -timeout 3m
        printf '%s\n' 'Checking parser behavior and the offline corpus contract...'
        "$corpus_python" -m unittest discover -s corpus -v
        "$corpus_python" -m unittest discover -s scripts -p 'test_*.py' -v
        "$corpus_python" corpus/corpus.py run --corpus corpus/smoke \
            --schemas generated --validator bin/cfn-cue-validate --limit 0 \
            --regions us-east-1,us-west-2 --resources none --timeout 15 --memory 2GiB \
            --expect corpus/smoke/expectations.json --report .corpus-smoke-report "$@"
        ;;
    fetch)
        require_environment
        "$corpus_python" corpus/corpus.py fetch --out "$corpus_data" --cache .corpus-cache "$@"
        ;;
    sample)
        require_environment
        require_downloads "$@"
        build_tools
        "$corpus_python" corpus/corpus.py run --corpus "$corpus_data" \
            --schemas generated --validator bin/cfn-cue-validate \
            --regions us-east-1 --limit 50 --timeout 6 --memory 2GiB \
            --resources none --report .corpus-sample-report "$@"
        ;;
    full)
        require_environment
        require_downloads "$@"
        build_tools
        full "$@"
        ;;
    resume)
        require_environment
        require_downloads "$@"
        [ -x bin/cfn-cue-validate ] || die 'The original validator binary is missing; run full to start a new report.'
        # Do not rebuild: the journal verifies this exact binary's checksum.
        full --resume "$@"
        ;;
    diagnose)
        require_environment
        require_downloads "$@"
        build_tools
        "$corpus_python" corpus/corpus.py run --corpus "$corpus_data" \
            --schemas generated --validator bin/cfn-cue-validate --regions us-east-1 \
            --ids 00611feb1a1433ed,0830d3c308162dd1,15c2cb3e41e6d416,6295685ddc4ca48e \
            --limit 0 --timeout 10 --memory 2GiB --resources all --max-resource-checks 0 \
            --report .corpus-resources-report "$@"
        ;;
    schemas)
        build_tools
        bin/cfn-cue -source snapshots/schemas-2026-10-12.zip -out .corpus-generated "$@"
        ;;
    regenerate)
        require_environment
        require_downloads "$@"
        "$corpus_python" corpus/make_smoke.py --corpus "$corpus_data" "$@"
        ;;
    flip)
        require_environment
        "$corpus_python" corpus/check_environment.py corpus/requirements-flip.txt
        "$corpus_python" corpus/corpus.py fetch --cache .corpus-cache \
            --compare-flip --out .corpus-flip "$@"
        ;;
    *)
        help >&2
        die "Unknown command: $corpus_command"
        ;;
esac
