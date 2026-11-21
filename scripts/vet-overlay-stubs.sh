#!/bin/sh
set -eu
root=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail=0
while IFS= read -r stub; do
	[ -n "$stub" ] || continue
	base=$(basename "$stub")
	case "$base" in
	mutation_documentidentity.go)
		prod="$root/internal/caps/cdp/documentidentity.go"
		;;
	mutation_documentconfirm.go)
		prod="$root/internal/caps/cdp/documentconfirm.go"
		;;
	*)
		echo "vet-overlay-stubs: unknown stub $stub" >&2
		fail=1
		continue
		;;
	esac
	overlay="$tmp/$(echo "$base" | tr '/.' '__').json"
	python3 -c 'import json,sys; open(sys.argv[1],"w").write(json.dumps({"Replace":{sys.argv[2]:sys.argv[3]}}))' \
		"$overlay" "$prod" "$stub"
	if ! (cd "$root" && go test -c -o /dev/null -overlay "$overlay" ./internal/caps/cdp); then
		echo "vet-overlay-stubs: stub does not compile against production: $stub" >&2
		fail=1
	fi
done <<EOF
$root/test/integration/testdata/viewsourceattach/mutation_documentidentity.go
$root/test/integration/testdata/viewsourceattach/mutation_documentconfirm.go
EOF
exit "$fail"
