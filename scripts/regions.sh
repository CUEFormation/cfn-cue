#!/usr/bin/env sh
# Build the generator, then measure both regional representations offline.
set -eu
regions_repo=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
cd "$regions_repo"
case "${1:-help}" in
    benchmark)
        shift
        go build -trimpath -buildvcs=false -o bin/cfn-cue ./cmd/cfn-cue
        exec python3 scripts/region_performance.py "$@"
        ;;
    help|-h|--help)
        cat <<'EOF'
Usage: ./scripts/regions.sh benchmark [options]

Requires Go 1.26+, Python 3, and a CUE CLI compatible with the generated module.
Defaults: us-east-1; nested scales 2,10,50,all; 20 seconds per fresh CLI process;
one repeat; GOMEMLIMIT=2GiB; output .region-performance-report/.

Options: --cue PATH, --sizes 2,10,50,all, --timeout SECONDS, --repeats N,
         --region REGION, --template LONG_FORM_YAML, --out NEW_DIRECTORY.
Use --help after benchmark for advanced options. No examples are downloaded.
EOF
        ;;
    *)
        printf '%s\n' 'Use help or benchmark.' >&2
        exit 2
        ;;
esac
