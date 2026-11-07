#!/usr/bin/env bash
# One offline state machine for deployed Compose and isolated local verification.
set -Eeuo pipefail
umask 077
compose= data= output= health= local_server= local_pid= binary=
while (($#)); do
  case "$1" in
    --compose) compose=${2:?}; shift 2 ;;
    --data) data=${2:?}; shift 2 ;;
    --output) output=${2:?}; shift 2 ;;
    --health-url) health=${2:?}; shift 2 ;;
    --local-server) local_server=${2:?}; shift 2 ;;
    --local-pid-file) local_pid=${2:?}; shift 2 ;;
    --binary) binary=${2:?}; shift 2 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done
[[ $data = /* && $output = /* && $health =~ ^https?:// ]] || { echo 'Absolute --data/--output and --health-url required' >&2; exit 2; }
data=$(realpath -e -- "$data")
output=$(realpath -m -- "$output")
[[ $output != "$data" && $output != "$data/"* && $data != "$output/"* ]] || { echo 'Backup and data overlap' >&2; exit 2; }
if [[ -e $output ]]; then
  [[ -d $output ]] || { echo 'Backup output must be a directory' >&2; exit 2; }
  if ! output_entries=$(find "$output" -mindepth 1 -maxdepth 1 -print -quit); then
    echo 'Cannot inspect backup output directory' >&2
    exit 2
  fi
  [[ -z $output_entries ]] || { echo 'Backup output must be new or empty' >&2; exit 2; }
fi
control=${BACKUP_CONTROL_DIR:-$(dirname -- "$output")/.work}
mkdir -p -- "$control"
control=$(realpath -e -- "$control")
log=$(mktemp "$control/offline-XXXXXXXX.log")
exec > >(tee -a "$log") 2>&1
printf 'Offline backup diagnostics: %s\n' "$log"
mode=compose
if [[ -n $compose ]]; then
  [[ -z $local_server && -z $local_pid && -z $binary ]] || { echo 'Compose and local options are exclusive'; exit 2; }
  compose=$(realpath -e -- "$compose")
  dc=(docker compose -f "$compose")
  # Never log the parsed configuration: it may contain protected model values.
  mounted=$("${dc[@]}" config --format json | python3 -c 'import json,os,sys
c=json.load(sys.stdin);s=c["services"]["yuanfa"];v=[x for x in s["volumes"] if x["target"]=="/data"]
if len(v)!=1 or v[0]["type"]!="bind" or v[0].get("read_only"):raise SystemExit("yuanfa requires one writable bind mount for /data")
print(os.path.realpath(v[0]["source"]))')
  [[ $mounted = "$data" ]] || { echo 'Compose /data mount does not match --data'; exit 2; }
  mkdir -p -- "$output"
  [[ $(stat -c %u -- "$output") = 1000 && $(stat -c %a -- "$output") = 700 ]] || { echo 'Prepare empty backup output owned by UID 1000 with mode0700 before stopping service'; exit 2; }
  running=$("${dc[@]}" ps --status running --services)
  was_running=0
  while IFS= read -r service; do [[ $service != yuanfa ]] || was_running=1; done <<< "$running"
else
  mode=local
  [[ -x $local_server && -x $binary && $local_pid = /* ]] || { echo 'Local mode requires executable --local-server/--binary and absolute --local-pid-file'; exit 2; }
  [[ $(realpath -e -- "${DATA_DIR:?Local DATA_DIR required}") = "$data" ]] || { echo 'Local DATA_DIR mismatch'; exit 2; }
  was_running=0
  if [[ -f $local_pid ]]; then
    pid=$(cat -- "$local_pid")
    [[ $pid =~ ^[0-9]+$ ]] || exit 2
    if kill -0 "$pid" 2>/dev/null; then
      [[ $(readlink -f "/proc/$pid/exe") = $(realpath -e -- "$local_server") ]] || { echo 'Local PID executable mismatch'; exit 2; }
      # Compare the target process DATA_DIR without emitting its environment.
      python3 -c 'import os,sys
v=dict(x.split(b"=",1) for x in open("/proc/"+sys.argv[1]+"/environ","rb").read().split(b"\0") if b"=" in x)
assert os.path.realpath(os.fsdecode(v[b"DATA_DIR"]))==sys.argv[2]' "$pid" "$data"
      was_running=1
    fi
  fi
fi
printf 'mode=%s original_running=%s data=%s output=%s\n' "$mode" "$was_running" "$data" "$output"
health_check() {
  local n path
  for path in live ready; do
    for ((n=0;n<60;n++)); do
      if curl --silent --show-error --fail --max-time 3 "${health%/}/health/$path"; then break; fi
      sleep 1
    done
    ((n<60)) || return 1
  done
}
restore_service() {
  local original=$? recovery=0
  trap - EXIT INT TERM
  if ((was_running)); then
    if [[ $mode = compose ]]; then
      "${dc[@]}" start yuanfa || recovery=$?
    else
      # Local-only proof: launch the exact Go executable, no persistent Node.
      setsid "$local_server" serve >> "$control/local-service.log" 2>&1 < /dev/null &
      printf '%s\n' "$!" > "$local_pid"
    fi
    health_check || recovery=1
    if ((recovery)); then echo 'Original service recovery failed'; original=1; fi
  fi
  printf 'Offline backup exit=%s; service recovery=%s\n' "$original" "$recovery"
  exit "$original"
}
trap restore_service EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
if ((was_running)); then
  if [[ $mode = compose ]]; then
    "${dc[@]}" stop -t 330 yuanfa
    running=$("${dc[@]}" ps --status running --services)
    while IFS= read -r service; do [[ $service != yuanfa ]] || { echo 'Service still running after stop'; exit 1; }; done <<< "$running"
  else
    kill -TERM "$pid"
    for ((n=0;n<330;n++)); do
      state=$(python3 -c 'import sys
try:print(open("/proc/"+sys.argv[1]+"/stat").read().rsplit(") ",1)[1].split()[0])
except FileNotFoundError:print("absent")' "$pid")
      [[ $state != Z && $state != absent ]] || break
      sleep 1
    done
    ((n<330)) || { echo 'Local Go failed to stop'; exit 1; }
  fi
fi
mkdir -p -- "$output"
if [[ $mode = compose ]]; then
  "${dc[@]}" run --rm --no-deps --entrypoint /app/yuanfa-backup -v "$output:/backup" yuanfa create --data /data --output /backup
  "${dc[@]}" run --rm --no-deps --entrypoint /app/yuanfa-backup -v "$output:/backup:ro" yuanfa verify --backup /backup
else
  "$binary" create --data "$data" --output "$output"
  "$binary" verify --backup "$output"
fi
