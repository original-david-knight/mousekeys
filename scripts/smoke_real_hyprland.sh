#!/usr/bin/env bash
# Real Hyprland/session smoke for the installed systemd user service.
#
# No-session runs print JSON status=skip. When a live Hyprland IPC socket is
# detectable, missing prerequisites or failed smoke assertions print
# status=fail and exit non-zero.
#
# Live prerequisites: go, git, hyprctl, systemctl, install, python3, and one
# smoke-only input injector from wtype, ydotool, or dotool.
set -u -o pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_path="${MOUSEKEYS_INSTALL_PATH:-$HOME/.local/bin/mousekeys}"
install_dir="$(dirname "$install_path")"
result_path="${MOUSEKEYS_SMOKE_RESULT:-}"
trace_path="${MOUSEKEYS_SMOKE_TRACE:-${XDG_RUNTIME_DIR:-/tmp}/mousekeys-real-smoke-$(date -u +%Y%m%dT%H%M%SZ)-$$.jsonl}"
unit_path="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/mousekeys.service"
python_bin="${PYTHON:-python3}"
checks_file=""
input_tool=""
expected_version="${MOUSEKEYS_SMOKE_VERSION:-real-smoke}"
expected_commit="unknown"
expected_build_date=""
installed_sha=""
trace_env_touched=0
old_trace_env_state="unset"
old_trace_env_value=""

write_json() {
	local json="$1"
	if [ -n "$result_path" ]; then
		mkdir -p "$(dirname "$result_path")"
		printf '%s\n' "$json" >"$result_path"
	fi
	printf '%s\n' "$json"
}

static_skip_no_session() {
	write_json '{"status":"skip","reason":"no live Hyprland session detected","checks":[{"name":"hyprland_session","status":"skip","details":"missing required Hyprland environment or IPC socket"}]}'
	exit 0
}

static_fail() {
	write_json '{"status":"fail","reason":"live Hyprland session detected but the smoke harness prerequisite check failed","checks":[{"name":"harness_prerequisites","status":"fail","details":"python3 is required for JSON status and trace assertions"}]}'
	exit 1
}

cleanup() {
	if [ -n "$checks_file" ]; then
		rm -f "$checks_file"
	fi
	if [ "$trace_env_touched" = "1" ] && command -v systemctl >/dev/null 2>&1; then
		if [ "$old_trace_env_state" = "set" ]; then
			systemctl --user set-environment "MOUSEKEYS_TRACE_JSONL=$old_trace_env_value" >/dev/null 2>&1 || true
		else
			systemctl --user unset-environment MOUSEKEYS_TRACE_JSONL >/dev/null 2>&1 || true
		fi
	fi
	if [ -n "$install_dir" ]; then
		PATH="$install_dir:$PATH" mousekeys hide >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT

add_check() {
	local name="$1"
	local status="$2"
	local details="$3"
	"$python_bin" - "$checks_file" "$name" "$status" "$details" <<'PY'
import json
import sys

path, name, status, details = sys.argv[1:5]
with open(path, "a", encoding="utf-8") as f:
    f.write(json.dumps({"name": name, "status": status, "details": details}, sort_keys=True) + "\n")
PY
}

finish() {
	local status="$1"
	local reason="$2"
	local output
	output="$("$python_bin" - "$checks_file" "$status" "$reason" "$install_path" "$trace_path" "$input_tool" "$expected_version" "$expected_commit" "$expected_build_date" "$installed_sha" <<'PY'
import json
import sys

checks_path, status, reason, install_path, trace_path, input_tool, version, commit, build_date, sha = sys.argv[1:11]
checks = []
try:
    with open(checks_path, encoding="utf-8") as f:
        checks = [json.loads(line) for line in f if line.strip()]
except FileNotFoundError:
    pass
doc = {
    "status": status,
    "reason": reason,
    "install_path": install_path,
    "trace_path": trace_path,
    "input_tool": input_tool or None,
    "expected_build": {
        "version": version,
        "commit": commit,
        "build_date": build_date,
        "installed_sha256": sha or None,
    },
    "checks": checks,
}
print(json.dumps(doc, indent=2, sort_keys=True))
PY
)"
	write_json "$output"
	case "$status" in
		pass|skip) exit 0 ;;
		*) exit 1 ;;
	esac
}

fail_check() {
	local name="$1"
	local details="$2"
	add_check "$name" "fail" "$details"
	finish "fail" "$details"
}

require_command() {
	local command_name="$1"
	if ! command -v "$command_name" >/dev/null 2>&1; then
		fail_check "prerequisite_$command_name" "missing required command '$command_name' while a live Hyprland session is available"
	fi
	add_check "prerequisite_$command_name" "pass" "$(command -v "$command_name")"
}

if [ -z "${XDG_RUNTIME_DIR:-}" ] || [ -z "${WAYLAND_DISPLAY:-}" ] || [ -z "${HYPRLAND_INSTANCE_SIGNATURE:-}" ]; then
	static_skip_no_session
fi

hypr_socket="$XDG_RUNTIME_DIR/hypr/$HYPRLAND_INSTANCE_SIGNATURE/.socket.sock"
if [ ! -S "$hypr_socket" ]; then
	static_skip_no_session
fi

if ! command -v "$python_bin" >/dev/null 2>&1; then
	static_fail
fi
checks_file="$(mktemp)"

add_check "hyprland_session" "pass" "detected Hyprland IPC socket at $hypr_socket"
require_command go
require_command git
require_command hyprctl
require_command systemctl
require_command install

for candidate in wtype ydotool dotool; do
	if command -v "$candidate" >/dev/null 2>&1; then
		input_tool="$candidate"
		break
	fi
done
if [ -z "$input_tool" ]; then
	fail_check "input_tool" "missing test input tool; install one of wtype, ydotool, or dotool for real-session key injection"
fi
add_check "input_tool" "pass" "using $input_tool for smoke-only key injection"

monitors_file="$(mktemp)"
if ! hyprctl -j monitors >"$monitors_file" 2>"$monitors_file.err"; then
	fail_check "hyprctl_monitors" "hyprctl -j monitors failed: $(cat "$monitors_file.err")"
fi
if ! monitor_summary="$("$python_bin" - "$monitors_file" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    monitors = json.load(f)
focused = [m for m in monitors if m.get("focused")]
if not focused:
    raise SystemExit("no focused monitor in hyprctl -j monitors output")
m = focused[0]
scale = float(m.get("scale") or 1)
logical_w = round(int(m["width"]) / scale)
logical_h = round(int(m["height"]) / scale)
print(f'focused={m.get("name")} origin=({m.get("x",0)},{m.get("y",0)}) logical={logical_w}x{logical_h} scale={scale:g}')
PY
)"; then
	fail_check "hyprctl_monitors" "could not parse focused monitor from hyprctl -j monitors"
fi
add_check "hyprctl_monitors" "pass" "$monitor_summary"
rm -f "$monitors_file" "$monitors_file.err"

expected_commit="$(git -C "$repo_root" rev-parse --short=12 HEAD 2>/dev/null || printf unknown)"
if ! git -C "$repo_root" diff --quiet --ignore-submodules -- 2>/dev/null || ! git -C "$repo_root" diff --cached --quiet --ignore-submodules -- 2>/dev/null; then
	expected_commit="$expected_commit-dirty"
fi
expected_build_date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
tmp_bin="$(mktemp)"
ldflags="-X main.version=$expected_version -X main.commit=$expected_commit -X main.buildDate=$expected_build_date"
if ! (cd "$repo_root" && go build -trimpath -ldflags "$ldflags" -o "$tmp_bin" .); then
	rm -f "$tmp_bin"
	fail_check "build_install" "go build for installed smoke binary failed"
fi
mkdir -p "$install_dir"
if ! install -m 0755 "$tmp_bin" "$install_path"; then
	rm -f "$tmp_bin"
	fail_check "build_install" "install rebuilt binary to $install_path failed"
fi
rm -f "$tmp_bin"
if ! installed_sha="$("$python_bin" - "$install_path" <<'PY'
import hashlib
import sys

h = hashlib.sha256()
with open(sys.argv[1], "rb") as f:
    for chunk in iter(lambda: f.read(1024 * 1024), b""):
        h.update(chunk)
print(h.hexdigest())
PY
)"; then
	fail_check "build_install" "could not compute sha256 for installed binary $install_path"
fi
add_check "build_install" "pass" "built version=$expected_version commit=$expected_commit build_date=$expected_build_date and installed $install_path sha256=$installed_sha"

mkdir -p "$(dirname "$unit_path")"
if ! install -m 0644 "$repo_root/mousekeys.service" "$unit_path"; then
	fail_check "service_unit_install" "install mousekeys.service to $unit_path failed"
fi
if ! systemctl --user daemon-reload; then
	fail_check "service_unit_install" "systemctl --user daemon-reload failed"
fi
add_check "service_unit_install" "pass" "installed checked-in user unit at $unit_path"

old_trace_line="$(systemctl --user show-environment 2>/dev/null | grep -m1 '^MOUSEKEYS_TRACE_JSONL=' || true)"
if [ -n "$old_trace_line" ]; then
	old_trace_env_state="set"
	old_trace_env_value="${old_trace_line#MOUSEKEYS_TRACE_JSONL=}"
fi
trace_env_touched=1

mkdir -p "$(dirname "$trace_path")"
: >"$trace_path"
if ! systemctl --user import-environment XDG_RUNTIME_DIR WAYLAND_DISPLAY HYPRLAND_INSTANCE_SIGNATURE; then
	fail_check "service_environment" "systemctl --user import-environment for Hyprland variables failed"
fi
if ! systemctl --user set-environment "MOUSEKEYS_INSTALL_PATH=$install_path" "MOUSEKEYS_TRACE_JSONL=$trace_path"; then
	fail_check "service_environment" "systemctl --user set-environment for MOUSEKEYS_INSTALL_PATH and MOUSEKEYS_TRACE_JSONL failed"
fi
add_check "service_environment" "pass" "imported Hyprland env and set MOUSEKEYS_INSTALL_PATH=$install_path with trace=$trace_path"

assert_status_json() {
	local status_file="$1"
	local systemd_pid="$2"
	"$python_bin" - "$status_file" "$expected_version" "$expected_commit" "$expected_build_date" "$installed_sha" "$install_path" "$systemd_pid" <<'PY'
import json
import sys

status_path, version, commit, build_date, installed_sha, install_path, systemd_pid = sys.argv[1:8]
with open(status_path, encoding="utf-8") as f:
    status = json.load(f)
errors = []
pid = int(status.get("pid") or 0)
if pid <= 0:
    errors.append(f"invalid daemon pid {pid}")
try:
    main_pid = int(systemd_pid or 0)
except ValueError:
    main_pid = 0
if main_pid > 0 and pid != main_pid:
    errors.append(f"status pid {pid} != systemd MainPID {main_pid}")
build = status.get("build") or {}
for key, want in [("version", version), ("commit", commit), ("build_date", build_date)]:
    if build.get(key) != want:
        errors.append(f"build.{key}={build.get(key)!r}, want {want!r}")
expected_build_id = "|".join([
    version,
    commit,
    build_date,
    str(build.get("go_version", "")),
    str(build.get("goos", "")),
    str(build.get("goarch", "")),
])
if status.get("build_id") != expected_build_id:
    errors.append(f"build_id={status.get('build_id')!r}, want {expected_build_id!r}")
if (status.get("service") or {}).get("manager") != "systemd-user":
    errors.append(f"service.manager={(status.get('service') or {}).get('manager')!r}, want systemd-user")
binary = status.get("binary") or {}
client = status.get("client") or {}
process_sha = ((binary.get("process_file") or {}).get("sha256") or "")
path_sha = ((binary.get("path_file") or {}).get("sha256") or "")
client_sha = ((client.get("process_file") or {}).get("sha256") or "")
if installed_sha not in {process_sha, path_sha}:
    errors.append("daemon binary sha256 does not match rebuilt installed binary")
if client_sha and client_sha != installed_sha:
    errors.append("mousekeys status client sha256 does not match rebuilt installed binary")
if errors:
    print("; ".join(errors))
    raise SystemExit(1)
active = status.get("active")
print(f"pid={pid} active={active} build_id={status.get('build_id')} process_sha256={process_sha or path_sha} install_path={install_path}")
PY
}

wait_installed_service_status() {
	local check_name="$1"
	local last="status was not read"
	for _ in $(seq 1 60); do
		local status_file
		local err_file
		local systemd_pid
		status_file="$(mktemp)"
		err_file="$(mktemp)"
		systemd_pid="$(systemctl --user show mousekeys.service --property=MainPID --value 2>/dev/null || true)"
		if PATH="$install_dir:$PATH" mousekeys status >"$status_file" 2>"$err_file"; then
			local summary
			if summary="$(assert_status_json "$status_file" "$systemd_pid" 2>&1)"; then
				rm -f "$status_file" "$err_file"
				add_check "$check_name" "pass" "$summary"
				return 0
			fi
			last="$summary"
		else
			last="$(cat "$err_file")"
		fi
		rm -f "$status_file" "$err_file"
		sleep 0.2
	done
	fail_check "$check_name" "mousekeys status did not report the rebuilt systemd service: $last"
}

restart_service() {
	local check_name="$1"
	if ! systemctl --user restart mousekeys.service; then
		fail_check "$check_name" "systemctl --user restart mousekeys.service failed"
	fi
	wait_installed_service_status "$check_name"
}

status_active_is() {
	local want="$1"
	local status_file
	status_file="$(mktemp)"
	if ! PATH="$install_dir:$PATH" mousekeys status >"$status_file" 2>/dev/null; then
		rm -f "$status_file"
		return 1
	fi
	"$python_bin" - "$status_file" "$want" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    status = json.load(f)
want = sys.argv[2].lower() == "true"
raise SystemExit(0 if bool(status.get("active")) == want else 1)
PY
	local rc=$?
	rm -f "$status_file"
	return "$rc"
}

wait_status_active() {
	local want="$1"
	for _ in $(seq 1 30); do
		if status_active_is "$want"; then
			return 0
		fi
		sleep 0.1
	done
	return 1
}

hide_overlay() {
	PATH="$install_dir:$PATH" mousekeys hide >/dev/null 2>&1 || true
}

trace_line_count() {
	if [ ! -f "$trace_path" ]; then
		printf '0\n'
		return
	fi
	wc -l <"$trace_path" | tr -d ' '
}

trace_has_events() {
	local start_line="$1"
	shift
	"$python_bin" - "$trace_path" "$start_line" "$@" <<'PY'
import json
import sys

path = sys.argv[1]
start = int(sys.argv[2])
wants = set(sys.argv[3:])
events = []
try:
    with open(path, encoding="utf-8") as f:
        for i, line in enumerate(f):
            if i < start or not line.strip():
                continue
            events.append(json.loads(line).get("event"))
except FileNotFoundError:
    pass
seen = set(events)
missing = sorted(wants - seen)
if missing:
    print("missing " + ",".join(missing))
    raise SystemExit(1)
print("seen " + ",".join(sorted(wants)))
PY
}

wait_trace_events() {
	local start_line="$1"
	local timeout_loops="$2"
	shift 2
	local last="no trace events"
	for _ in $(seq 1 "$timeout_loops"); do
		if last="$(trace_has_events "$start_line" "$@" 2>&1)"; then
			return 0
		fi
		sleep 0.1
	done
	printf '%s\n' "$last"
	return 1
}

assert_flow_trace() {
	local start_line="$1"
	local cursorpos="$2"
	"$python_bin" - "$trace_path" "$start_line" "$cursorpos" <<'PY'
import json
import math
import re
import sys

path = sys.argv[1]
start = int(sys.argv[2])
cursorpos = sys.argv[3]
events = []
try:
    with open(path, encoding="utf-8") as f:
        for i, line in enumerate(f):
            if i < start or not line.strip():
                continue
            event = json.loads(line)
            event["_line"] = i + 1
            events.append(event)
except FileNotFoundError:
    events = []
required = {
    "overlay.surface_create",
    "overlay.keyboard_grab",
    "keyboard.keymap",
    "keyboard.enter",
    "keyboard.key",
    "keyboard.token",
    "coordinate.selected_cell",
    "pointer.motion",
    "overlay.unmapped_for_click",
    "pointer.button",
    "click_group.complete",
    "stay_active.reset",
}
seen = {e.get("event") for e in events}
missing = sorted(required - seen)
if missing:
    print("missing trace events: " + ",".join(missing))
    raise SystemExit(1)
tokens = [e.get("fields") or {} for e in events if e.get("event") == "keyboard.token"]
letters = [t.get("letter") for t in tokens if t.get("kind") == "letter"]
if letters[:2] != ["M", "K"]:
    print(f"keyboard token letters={letters!r}, want ['M', 'K']")
    raise SystemExit(1)
if not any(t.get("kind") == "command" and t.get("command") == "left_click" for t in tokens):
    print("missing left_click keyboard token for Space")
    raise SystemExit(1)
selected_events = [e for e in events if e.get("event") == "coordinate.selected_cell"]
selected = selected_events[-1]
fields = selected.get("fields") or {}
if fields.get("coordinate") != "MK" or fields.get("column") != 12 or fields.get("row") != 10:
    print(f"selected cell fields={fields!r}, want coordinate MK column=12 row=10")
    raise SystemExit(1)
cx = float(fields.get("center_virtual_x"))
cy = float(fields.get("center_virtual_y"))
numbers = re.findall(r"-?\d+(?:\.\d+)?", cursorpos)
if len(numbers) < 2:
    print(f"could not parse hyprctl cursorpos output {cursorpos!r}")
    raise SystemExit(1)
px, py = map(float, numbers[:2])
distance = math.hypot(px - cx, py - cy)
if distance > 3.0:
    print(f"cursorpos=({px:g},{py:g}) is {distance:.2f}px from selected center ({cx:g},{cy:g})")
    raise SystemExit(1)
unmap_lines = [e["_line"] for e in events if e.get("event") == "overlay.unmapped_for_click"]
button_lines = [e["_line"] for e in events if e.get("event") == "pointer.button"]
if not unmap_lines or not button_lines or min(unmap_lines) > min(button_lines):
    print(f"overlay unmap lines={unmap_lines}, pointer button lines={button_lines}; want unmap before button")
    raise SystemExit(1)
print(f"coordinate=MK cursorpos=({px:g},{py:g}) selected_center=({cx:g},{cy:g}) distance={distance:.2f}px buttons={len(button_lines)} unmap_line={min(unmap_lines)} first_button_line={min(button_lines)}")
PY
}

wait_and_assert_flow_trace() {
	local start_line="$1"
	local check_name="$2"
	local last="flow trace not complete"
	for _ in $(seq 1 80); do
		local cursor
		if cursor="$(hyprctl cursorpos 2>&1)"; then
			local summary
			if summary="$(assert_flow_trace "$start_line" "$cursor" 2>&1)"; then
				add_check "$check_name" "pass" "$summary"
				return 0
			fi
			last="$summary"
		else
			last="hyprctl cursorpos failed: $cursor"
		fi
		sleep 0.1
	done
	fail_check "$check_name" "$last"
}

inject_mk_space() {
	case "$input_tool" in
		wtype)
			wtype m >/dev/null 2>&1 || return 1
			sleep 0.05
			wtype k >/dev/null 2>&1 || return 1
			sleep 0.05
			wtype -k space >/dev/null 2>&1 || return 1
			;;
		ydotool)
			ydotool type -d 20 mk >/dev/null 2>&1 || return 1
			sleep 0.05
			ydotool key 57:1 57:0 >/dev/null 2>&1 || return 1
			;;
		dotool)
			printf 'type mk\nkey space\n' | dotool >/dev/null 2>&1 || return 1
			;;
		*)
			return 1
			;;
	esac
}

inject_super_period() {
	case "$input_tool" in
		wtype)
			wtype -M logo -k period -m logo >/dev/null 2>&1
			;;
		ydotool)
			ydotool key 125:1 52:1 52:0 125:0 >/dev/null 2>&1
			;;
		dotool)
			printf 'keydown super\nkey period\nkeyup super\n' | dotool >/dev/null 2>&1
			;;
		*)
			return 1
			;;
	esac
}

trigger_show() {
	local trigger="$1"
	case "$trigger" in
		hyprctl)
			hyprctl dispatch exec "mousekeys show" >/dev/null
			;;
		ipc)
			PATH="$install_dir:$PATH" mousekeys show >/dev/null
			;;
		keybind)
			inject_super_period
			;;
		*)
			return 1
			;;
	esac
}

run_flow() {
	local check_name="$1"
	local trigger="$2"
	hide_overlay
	wait_status_active false >/dev/null 2>&1 || true
	local start_line
	start_line="$(trace_line_count)"
	if ! trigger_show "$trigger"; then
		fail_check "$check_name" "failed to trigger overlay using $trigger"
	fi
	local focus_wait
	if ! focus_wait="$(wait_trace_events "$start_line" 80 overlay.keyboard_grab keyboard.keymap keyboard.enter 2>&1)"; then
		fail_check "$check_name" "overlay did not receive keyboard focus/keymap after $trigger trigger: $focus_wait"
	fi
	if ! inject_mk_space; then
		fail_check "$check_name" "failed to inject M K Space with $input_tool"
	fi
	wait_and_assert_flow_trace "$start_line" "$check_name"
	hide_overlay
	if ! wait_status_active false; then
		fail_check "${check_name}_cleanup" "overlay stayed active after cleanup hide"
	fi
}

exercise_hide_show_show() {
	hide_overlay
	wait_status_active false >/dev/null 2>&1 || true
	local start_line
	start_line="$(trace_line_count)"
	if ! PATH="$install_dir:$PATH" mousekeys show >/dev/null; then
		fail_check "hide_show_show_reuse" "mousekeys show failed during reuse setup"
	fi
	if ! wait_trace_events "$start_line" 80 overlay.keyboard_grab keyboard.keymap keyboard.enter >/dev/null 2>&1; then
		fail_check "hide_show_show_reuse" "first mousekeys show did not create focused keyboard overlay"
	fi
	if ! PATH="$install_dir:$PATH" mousekeys show >/dev/null; then
		fail_check "hide_show_show_reuse" "second mousekeys show failed to toggle overlay"
	fi
	if ! wait_status_active false; then
		fail_check "hide_show_show_reuse" "second mousekeys show did not toggle overlay inactive"
	fi
	add_check "hide_show_show_reuse" "pass" "mousekeys hide; mousekeys show; mousekeys show toggled cleanly before the repeated flow"
	run_flow "after_hide_show_show_flow" "hyprctl"
}

detect_super_period_keybind() {
	local binds_file
	binds_file="$(mktemp)"
	if ! hyprctl binds -j >"$binds_file" 2>"$binds_file.err"; then
		printf 'skip:hyprctl binds -j failed: %s\n' "$(cat "$binds_file.err")"
		rm -f "$binds_file" "$binds_file.err"
		return
	fi
	"$python_bin" - "$binds_file" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    binds = json.load(f)
matches = []
for bind in binds:
    arg = str(bind.get("arg") or "")
    dispatcher = str(bind.get("dispatcher") or "")
    if "mousekeys" in arg and "show" in arg and dispatcher in {"exec", "execr"}:
        matches.append(bind)
if not matches:
    print("skip:hyprctl binds -j has no exec binding whose arg contains mousekeys show")
    raise SystemExit
for bind in matches:
    key = str(bind.get("key") or "").lower()
    try:
        modmask = int(bind.get("modmask") or 0)
    except (TypeError, ValueError):
        modmask = 0
    if key in {"period", "."} and (modmask & 64):
        print(f'possible:key={bind.get("key")} modmask={modmask} arg={bind.get("arg")}')
        raise SystemExit
print("skip:mousekeys show binding exists, but this harness only auto-synthesizes SUPER+period")
PY
	rm -f "$binds_file" "$binds_file.err"
}

run_keybind_flow_if_possible() {
	local detection
	detection="$(detect_super_period_keybind)"
	case "$detection" in
		possible:*)
			add_check "configured_keybind_detected" "pass" "${detection#possible:}"
			run_flow "configured_keybind_flow" "keybind"
			;;
		skip:*)
			add_check "configured_keybind_flow" "skip" "${detection#skip:}"
			;;
		*)
			add_check "configured_keybind_flow" "skip" "could not determine configured keybind path"
			;;
	esac
}

find_chrome_client() {
	local clients_file
	clients_file="$(mktemp)"
	if ! hyprctl -j clients >"$clients_file" 2>"$clients_file.err"; then
		printf 'skip:hyprctl -j clients failed: %s\n' "$(cat "$clients_file.err")"
		rm -f "$clients_file" "$clients_file.err"
		return
	fi
	"$python_bin" - "$clients_file" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    clients = json.load(f)
needles = ("chrome", "chromium")
for client in clients:
    haystack = " ".join(str(client.get(k) or "") for k in ("class", "initialClass", "title", "initialTitle")).lower()
    if any(n in haystack for n in needles):
        print(f'found:{client.get("address")}:{client.get("class") or client.get("initialClass") or "unknown"}:{client.get("title") or ""}')
        raise SystemExit
print("skip:no Chrome or Chromium client was present in hyprctl -j clients")
PY
	rm -f "$clients_file" "$clients_file.err"
}

run_chrome_flow_if_available() {
	local detection
	detection="$(find_chrome_client)"
	case "$detection" in
		found:*)
			local rest address klass title
			rest="${detection#found:}"
			address="${rest%%:*}"
			rest="${rest#*:}"
			klass="${rest%%:*}"
			title="${rest#*:}"
			if [ -z "$address" ] || [ "$address" = "None" ]; then
				fail_check "chrome_focused_flow" "Chrome/Chromium client was found but had no address"
			fi
			if ! hyprctl dispatch focuswindow "address:$address" >/dev/null; then
				fail_check "chrome_focused_flow" "failed to focus Chrome/Chromium client address:$address"
			fi
			sleep 0.2
			add_check "chrome_focus" "pass" "focused Chrome/Chromium client address=$address class=$klass title=$title"
			run_flow "chrome_focused_flow" "hyprctl"
			;;
		skip:*)
			add_check "chrome_focused_flow" "skip" "${detection#skip:}"
			;;
		*)
			add_check "chrome_focused_flow" "skip" "could not determine Chrome/Chromium availability"
			;;
	esac
}

restart_service "service_restart_initial_status"
run_flow "hyprctl_dispatch_exec_flow" "hyprctl"
run_keybind_flow_if_possible
exercise_hide_show_show
restart_service "service_restart_rebuilt_status"
run_flow "after_service_restart_flow" "hyprctl"
run_chrome_flow_if_available

finish "pass" "real Hyprland installed-service smoke completed"
