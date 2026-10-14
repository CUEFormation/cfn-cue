#!/bin/bash
# Benchmarks template validation through regions/us-east-1 #Template for
# regions of increasing size. Writes RESULT lines to $OUT/results.txt.
#
#   scripts/region-bench.sh                  the current generator
#   OLD_REF=<git ref> scripts/region-bench.sh  also the generator at that ref
#
# For example, OLD_REF=671dded measures the #Resource union that preceded
# selection by Type, and OLD_REF=f01c05f the optional-field intrinsics.
#
# TYPES lists region sizes (default "6 50 200 800 1839"); SIZES lists
# template sizes (default 1,10,50); RUN_TIMEOUT caps each run in seconds.
set -u
REPO=$(cd "$(dirname "$0")/.." && pwd)
OUT=${OUT:-$(mktemp -d)}
SNAP=$REPO/snapshots/schemas-2026-10-12.zip
TYPES=${TYPES:-6 50 200 800 1839}
SIZES=${SIZES:-1,10,50}
RUN_TIMEOUT=${RUN_TIMEOUT:-600}
VARIANTS="current"
if [ -n "${OLD_REF:-}" ]; then
	git -C "$REPO" worktree add -q "$OUT/old-src" "$OLD_REF" || exit 1
	trap 'git -C "$REPO" worktree remove --force "$OUT/old-src"' EXIT
	VARIANTS="$VARIANTS old"
fi
# The six benchmark types first, then the rest of us-east-1 alphabetically.
(cd "$REPO" && go run ./cmd/cfn-cue -source "$SNAP" -out "$OUT/all" -regions us-east-1 >/dev/null) || exit 1
python3 - "$OUT/all/manifest.json" > "$OUT/types.txt" <<'EOF'
import json, sys
first = ["AWS::SQS::Queue", "AWS::IAM::Role", "AWS::EC2::Subnet", "AWS::S3::Bucket", "AWS::Lambda::Function", "AWS::DynamoDB::Table"]
rest = sorted(t for t in json.load(open(sys.argv[1]))["regions"]["us-east-1"] if t not in first)
print("\n".join(first + rest))
EOF
generate() { # variant types dir
	local src=$REPO
	[ "$1" = old ] && src=$OUT/old-src
	rm -rf "$3"
	(cd "$src" && go run ./cmd/cfn-cue -source "$SNAP" -out "$3" -regions us-east-1 -resources "$(head -n "$2" "$OUT/types.txt" | paste -sd, -)" >/dev/null)
}
for types in $TYPES; do
	for variant in $VARIANTS; do
		dir=$OUT/$variant-$types
		if ! generate "$variant" "$types" "$dir"; then
			echo "variant=$variant types=$types GENERATE_FAILED" | tee -a "$OUT/results.txt"
			continue
		fi
		(cd "$REPO" && CFN_CUE_PERF_DIR=$dir CFN_CUE_PERF_SIZES=$SIZES timeout "$RUN_TIMEOUT" \
			go test ./internal/generate -run TestRegionPerformance -count=1 -timeout 2h -v) > "$OUT/log-$variant-$types.txt" 2>&1
		rc=$?
		grep -o 'RESULT .*' "$OUT/log-$variant-$types.txt" | sed "s/^RESULT /variant=$variant /" | tee -a "$OUT/results.txt"
		[ $rc -eq 124 ] && echo "variant=$variant types=$types TIMEOUT after ${RUN_TIMEOUT}s" | tee -a "$OUT/results.txt"
		[ $rc -ne 0 ] && [ $rc -ne 124 ] && echo "variant=$variant types=$types EXIT=$rc" | tee -a "$OUT/results.txt"
	done
done
echo "results: $OUT/results.txt"
