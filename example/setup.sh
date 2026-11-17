#!/bin/bash
action=setup

set -Eeuo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
umask 077

name="example"
path="/opt/example"
conf_dir="${path}/conf"
unit="${name}.service"
unit_file="${conf_dir}/${unit}"
policy="${conf_dir}/${name}.conf"
unit_link="/etc/systemd/system/${unit}"
policy_link="/etc/sysusers.d/${name}.conf"
rotation_link="/etc/logrotate.d/${name}"
record="${conf_dir}/.mksvc-install"
runtime_wanted=no
runtime_dir="/run/${name}"

phase=preflight
trap 'echo "${phase} failed at line ${LINENO}: ${BASH_COMMAND}" >&2' ERR

refuse() {
	echo "${phase}: $*" >&2
	exit 1
}

# A dangling symlink still occupies its path.
exists() {
	[[ -e $1 || -L $1 ]]
}

check_regular_file() {
	local file=$1

	if [[ -L $file || ! -f $file || $(stat -c %h -- "$file") != 1 ]]; then
		refuse "missing, linked or non-regular file: ${file}"
	fi
}

check_trusted_path() {
	local target=$1 metadata owner mode

	metadata=$(stat -c '%u %a' -- "$target")
	read -r owner mode <<< "$metadata"

	if [[ $owner != 0 ]] || (( (8#$mode & 0022) != 0 )); then
		refuse "must be root-owned without group/other write permission: ${target}"
	fi
}

check_directory_tree() {
	local dir=$1

	if [[ ! -d $dir || $(realpath -e -- "$dir") != "$dir" ]]; then
		refuse "not a canonical directory: ${dir}"
	fi

	while true; do
		check_trusted_path "$dir"

		if [[ $dir == / ]]; then
			break
		fi

		dir=$(dirname -- "$dir")
	done
}

check_link() {
	local link=$1 target=$2 parent

	parent=$(dirname -- "$link")
	if exists "$parent"; then
		check_directory_tree "$parent"
	fi

	if exists "$link" && [[ ! -L $link || $(readlink -- "$link") != "$target" ]]; then
		refuse "unrecognized installation at ${link} (expected a link to ${target})"
	fi
}

check_deployment() {
	local script="${conf_dir}/${action}.sh" dependency

	if [[ $EUID != 0 ]]; then
		refuse 'run this script as root'
	fi

	check_directory_tree "$conf_dir"

	if [[ $(realpath -e -- "${BASH_SOURCE[0]}") != "$script" ]]; then
		refuse "run ${script}"
	fi

	check_regular_file "$script"
	check_trusted_path "$script"

	if exists "${conf_dir}/.mksvc-recovery.json"; then
		refuse 'finish interrupted generation before continuing'
	fi

	check_link "$unit_link" "$unit_file"
	check_link "$policy_link" "$policy"
	check_link "$rotation_link" "${conf_dir}/${name}_logs.conf"

	# systemctl enable and disable also operate on dependency links.
	shopt -s nullglob
	for dependency in /{etc,run}/systemd/system/*.{wants,requires}/"$unit"; do
		if [[ ! -L $unit_link || ! -L $dependency || $(realpath -m -- "$dependency") != "$unit_file" ]]; then
			refuse "conflicting dependency: ${dependency}"
		fi
	done
	shopt -u nullglob
}

unit_property() {
	systemctl show --property="$1" --value "$unit"
}

# systemctl can return 4 along with a property value for a missing unit.
read_unit_state() {
	local fragment

	active=inactive
	enabled=''
	needs_reload=no
	loaded_runtime=''

	load_state=$(unit_property LoadState) || {
		if [[ $? != 4 || $load_state != not-found ]]; then
			refuse "cannot inspect ${unit}"
		fi
	}

	case "$load_state" in
		loaded)
			fragment=$(unit_property FragmentPath)
			if [[ ! -L $unit_link || ( $fragment != "$unit_file" && $fragment != "$unit_link" ) ]]; then
				refuse "${unit} belongs to another installation: ${fragment}"
			fi

			active=$(unit_property ActiveState)
			enabled=$(unit_property UnitFileState)
			needs_reload=$(unit_property NeedDaemonReload)
			loaded_runtime=$(unit_property RuntimeDirectory)
			;;
		not-found)
			active=$(unit_property ActiveState) || {
				if [[ $? != 4 || $active != inactive ]]; then
					refuse "cannot inspect ${unit}"
				fi
			}

			if [[ $active != inactive ]]; then
				refuse "${unit} is still in use"
			fi
			;;
		*)
			refuse "unexpected load state for ${unit}: ${load_state}"
			;;
	esac
}

save_record() (
	local fingerprint=$1 unit_digest=${2:-pending} tmp

	tmp=$(mktemp -- "${record}.XXXXXXXX")
	trap 'rm -f -- "$tmp"' EXIT

	printf 'v1 %s %s %s %s %s %s %s %s\n' "$path" "$name" "$origin" "$uid" "$gid" "$fingerprint" "$unit_digest" "$runtime_wanted" > "$tmp"
	mv -fT -- "$tmp" "$record"
)

valid_fingerprint() {
	[[ $1 == pending || $1 =~ ^[0-9a-f]{64}$ ]]
}

read_record() {
	local version deployment account extra

	origin=''
	saved_uid=''
	saved_gid=''
	applied=''
	applied_unit=''
	saved_runtime=''

	if ! exists "$record"; then
		return 0
	fi

	check_regular_file "$record"
	check_trusted_path "$record"
	IFS=' ' read -r version deployment account origin saved_uid saved_gid applied applied_unit saved_runtime extra < "$record"

	if [[ $version != v1 || $deployment != "$path" || $account != "$name" || -n $extra ]]; then
		refuse "invalid installation record: ${record}"
	fi

	if [[ $origin != created && $origin != legacy ]]; then
		refuse "invalid identity origin: ${record}"
	fi

	if [[ ! $saved_uid =~ ^[1-9][0-9]*$ || ! $saved_gid =~ ^[1-9][0-9]*$ ]]; then
		refuse "invalid identity in ${record}"
	fi

	if ! valid_fingerprint "$applied" || ! valid_fingerprint "$applied_unit"; then
		refuse "invalid fingerprint in ${record}"
	fi

	if [[ $saved_runtime != yes && $saved_runtime != no ]]; then
		refuse "invalid runtime flag in ${record}"
	fi
}

lookup() {
	# Only getent status 2 means the key is absent.
	getent "$1" "$2" || [[ $? == 2 ]]
}

dedicated_id() {
	[[ $1 =~ ^[1-9][0-9]*$ && $1 != 65534 ]]
}

read_identity() {
	local passwd_entry group_entry user primary home shell group members unused shared

	passwd_entry=$(lookup passwd "$name")
	group_entry=$(lookup group "$name")
	uid=''
	gid=''

	if [[ -n $group_entry ]]; then
		IFS=: read -r group unused gid members <<< "$group_entry"
		if [[ $group != "$name" || -n $members ]] || ! dedicated_id "$gid"; then
			refuse "group is not dedicated to ${name}"
		fi
	fi

	if [[ -n $passwd_entry ]]; then
		IFS=: read -r user unused uid primary unused home shell <<< "$passwd_entry"
		if [[ $user != "$name" || $primary != "$gid" || $home != "$path" ]] || ! dedicated_id "$uid"; then
			refuse "unexpected account for ${name}"
		fi

		if [[ $shell != /sbin/nologin && $shell != /usr/sbin/nologin ]]; then
			refuse "unexpected shell for ${name}"
		fi

		if [[ $(id -G "$name") != "$gid" ]]; then
			refuse "unexpected supplementary groups for ${name}"
		fi
	fi

	if [[ -z $gid ]]; then
		return 0
	fi

	shared=$(getent passwd | awk -F: -v name="$name" -v uid="$uid" -v gid="$gid" '$1 != name && ($4 == gid || $3 == uid) {print $1}')
	if [[ -n $shared ]]; then
		refuse "identity shared with ${shared}"
	fi

	shared=$(getent group | awk -F: -v name="$name" -v gid="$gid" '$1 != name && $3 == gid {print $1}')
	if [[ -n $shared ]]; then
		refuse "GID shared with ${shared}"
	fi
}

check_identity() {
	read_identity

	if [[ -n $origin ]]; then
		if [[ ( -n $uid && $uid != "$saved_uid" ) || ( -n $gid && $gid != "$saved_gid" ) ]]; then
			refuse 'recorded UID/GID no longer matches'
		fi

		if [[ $action == setup || $origin == legacy ]] && [[ -z $uid || -z $gid ]]; then
			refuse 'recorded identity is missing'
		fi
	elif [[ -n $uid || -n $gid ]]; then
		if [[ -z $uid || -z $gid || ! -L $policy_link || ! -L $unit_link || $load_state != loaded ]]; then
			refuse "refusing to adopt existing user or group ${name}"
		fi

		origin=legacy
	fi
}

check_runtime_dir() {
	if [[ $runtime_wanted != yes && $saved_runtime != yes && $loaded_runtime != "$name" ]]; then
		return 0
	fi

	if ! exists "$runtime_dir"; then
		return 0
	fi

	if [[ $saved_runtime != yes && ( $origin != legacy || $loaded_runtime != "$name" ) ]]; then
		refuse "runtime directory already in use: ${runtime_dir}"
	fi

	if [[ ! -d $runtime_dir || -L $runtime_dir || -z $uid || $(stat -c '%u:%g' -- "$runtime_dir") != "${uid}:${gid}" ]]; then
		refuse "unexpected runtime directory: ${runtime_dir}"
	fi
}

check_deployment
read_unit_state
read_record

if [[ $action == uninstall && -z $origin && ! -L $unit_link && ! -L $policy_link && ! -L $rotation_link ]]; then
	echo 'Already uninstalled.'
	exit 0
fi

check_identity
check_runtime_dir

binary="${path}/${name}"
policy_files=("$policy" "$unit_file" "${conf_dir}/${name}_logs.conf")
private_files=("${conf_dir}/setup.sh" "${conf_dir}/uninstall.sh")

if exists "${conf_dir}/svc.yml"; then
	private_files+=("${conf_dir}/svc.yml")
fi

for file in "$binary" "${policy_files[@]}" "${private_files[@]}"; do
	check_regular_file "$file"
	check_trusted_path "$file"
done

# Do not ask sysusers to interpret a stale policy for a different deployment.
expected_policy='u example - "Example Service" /opt/example /sbin/nologin'
actual_policy=$(awk 'NF && $1 !~ /^#/ {print}' "$policy")
if [[ $actual_policy != "$expected_policy" ]]; then
	refuse "sysusers policy does not match this deployment: ${policy}"
fi

check_directory_tree /etc/systemd/system
check_directory_tree /etc

parent=$(dirname -- "$path")
while [[ $parent != / ]]; do
	metadata=$(stat -c '%g %a' -- "$parent")
	read -r parent_gid mode <<< "$metadata"

	if [[ $parent_gid == "$gid" ]]; then
		traverse_bit=0010
	else
		traverse_bit=0001
	fi

	if (( (8#$mode & traverse_bit) == 0 )); then
		refuse "service cannot traverse ${parent}"
	fi

	parent=$(dirname -- "$parent")
done

for vendor_dir in /run/sysusers.d /usr/local/lib/sysusers.d /usr/lib/sysusers.d; do
	if exists "${vendor_dir}/${name}.conf"; then
		refuse "conflicting sysusers policy: ${vendor_dir}/${name}.conf"
	fi
done

if [[ -z $uid ]] && ! command -v systemd-sysusers >/dev/null; then
	refuse 'missing systemd-sysusers'
fi

log_dir="${path}/logs"
log_file="${log_dir}/${name}.log"

check_directory_tree /etc/logrotate.d

if exists "$log_dir"; then
	check_directory_tree "$log_dir"
fi

if exists "$log_file"; then
	check_regular_file "$log_file"
fi

shopt -s nullglob
archives=("${log_file}".[0-9]*)
shopt -u nullglob

for archive in "${archives[@]}"; do
	check_regular_file "$archive"
done

if ! command -v logrotate >/dev/null; then
	refuse 'file logging requires logrotate'
fi

logrotate --debug "${conf_dir}/${name}_logs.conf"

# Hash contents, not timestamps: regeneration and permission-only repairs do
# not by themselves require restarting an otherwise healthy service.
fingerprint=$(sha256sum -- "$binary" "$unit_file" | sha256sum)
fingerprint=${fingerprint%% *}
unit_digest=$(sha256sum -- "$unit_file")
unit_digest=${unit_digest%% *}

set_permissions() {
	local target=$1 wanted_owner=$2 wanted_mode=$3 metadata owner mode

	metadata=$(stat -c '%u:%g %a' -- "$target")
	read -r owner mode <<< "$metadata"

	if [[ $owner != "$wanted_owner" ]]; then
		chown -- "$wanted_owner" "$target"
		mode='' # chown can clear mode bits.
	fi

	if [[ $mode != "$wanted_mode" ]]; then
		chmod -- "=${wanted_mode}" "$target"
	fi
}

create_empty_file() (
	set -o noclobber
	: > "$1"
)

phase='setup (changes may already have been applied)'
restore_mounts=no
if [[ $runtime_wanted == yes && ! -d $runtime_dir ]]; then
	restore_mounts=yes
fi

set_permissions "$path" 0:0 755
set_permissions "$conf_dir" 0:0 755
set_permissions "$binary" 0:0 755

for file in "${private_files[@]}"; do
	set_permissions "$file" 0:0 700
done

for file in "${policy_files[@]}"; do
	set_permissions "$file" 0:0 644
done

if [[ ! -d /etc/sysusers.d ]]; then
	install -d -m 755 /etc/sysusers.d
fi

if [[ ! -L $policy_link ]]; then
	ln -sT -- "$policy" "$policy_link"
fi

if [[ -z $uid ]]; then
	systemd-sysusers "$policy_link"
	read_identity
	if [[ -z $uid || -z $gid ]]; then
		refuse 'sysusers did not create the expected identity'
	fi

	origin=created
fi

if [[ ! -f $record ]]; then
	save_record pending
fi

set_permissions "$record" 0:0 600

if [[ ! -d $log_dir ]]; then
	mkdir -- "$log_dir"
fi

set_permissions "$log_dir" "0:${gid}" 750

if [[ ! -f $log_file ]]; then
	create_empty_file "$log_file"
	restore_mounts=yes
fi

set_permissions "$log_file" "0:${gid}" 660

for archive in "${archives[@]}"; do
	set_permissions "$archive" "0:${gid}" 640
done

if [[ ! -L $rotation_link ]]; then
	ln -sT -- "${conf_dir}/${name}_logs.conf" "$rotation_link"
fi

if [[ ! -L $unit_link ]]; then
	ln -sT -- "$unit_file" "$unit_link"
	needs_reload=yes
fi

if [[ $needs_reload == yes || $load_state == not-found || $applied_unit != "$unit_digest" ]]; then
	systemctl daemon-reload
fi

if [[ $enabled != enabled || $applied_unit != "$unit_digest" ]]; then
	systemctl enable "$unit_file"
fi

if [[ $active != active ]]; then
	systemctl start "$unit"
elif [[ $applied != "$fingerprint" || $restore_mounts == yes ]]; then
	systemctl restart "$unit"
fi

if [[ $applied != "$fingerprint" || $saved_runtime != "$runtime_wanted" ]]; then
	save_record "$fingerprint" "$unit_digest"
fi

echo "Setup complete: ${unit}"
