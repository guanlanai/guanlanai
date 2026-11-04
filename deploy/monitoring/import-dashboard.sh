#!/usr/bin/env sh
set -eu

: "${GRAFANA_URL:?请设置 GRAFANA_URL，例如 https://your-stack.grafana.net}"
: "${GRAFANA_TOKEN:?请设置 GRAFANA_TOKEN（Grafana Service Account Token）}"

for command in curl jq; do
	if ! command -v "$command" >/dev/null 2>&1; then
		echo "缺少依赖: $command" >&2
		exit 1
	fi
done

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
dashboard="$script_dir/dashboards/guanlanai-overview.json"
if [ ! -f "$dashboard" ]; then
	echo "找不到看板文件: $dashboard" >&2
	exit 1
fi

payload=$(mktemp)
trap 'rm -f "$payload"' EXIT

jq '{dashboard: ., overwrite: true, message: "import guanlanai overview"}' "$dashboard" >"$payload"

curl -fsS \
	-H "Authorization: Bearer ${GRAFANA_TOKEN}" \
	-H 'Content-Type: application/json' \
	-X POST "${GRAFANA_URL%/}/api/dashboards/db" \
	--data-binary "@${payload}"
