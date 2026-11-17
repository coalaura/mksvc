#!/bin/bash
action=uninstall

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

check_policy() {
	local expected actual vendor_dir

	check_regular_file "$policy"
	check_trusted_path "$policy"

	expected='u example - "Example Service" /opt/example /sbin/nologin'
	actual=$(awk 'NF && $1 !~ /^#/ {print}' "$policy")
	if [[ $actual != "$expected" ]]; then
		refuse "sysusers policy does not match this deployment: ${policy}"
	fi

	for vendor_dir in /run/sysusers.d /usr/local/lib/sysusers.d /usr/lib/sysusers.d; do
		if exists "${vendor_dir}/${name}.conf"; then
			refuse "conflicting sysusers policy: ${vendor_dir}/${name}.conf"
		fi
	done
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
		if [[ -L $policy_link && $(getent -s files group "$name") != "$group_entry" ]]; then
			refuse "not a local group: ${name}"
		fi

		IFS=: read -r group unused gid members <<< "$group_entry"
		if [[ $group != "$name" || -n $members ]] || ! dedicated_id "$gid"; then
			refuse "group is not dedicated to ${name}"
		fi
	fi

	if [[ -n $passwd_entry ]]; then
		if [[ -L $policy_link && $(getent -s files passwd "$name") != "$passwd_entry" ]]; then
			refuse "not a local user: ${name}"
		fi

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
	if [[ $action == setup || -L $policy_link ]]; then
		check_policy
	fi

	read_identity

	if [[ $action == setup && ( -n $uid || -n $gid ) ]]; then
		if [[ ! -L $policy_link || -z $uid || -z $gid ]]; then
			refuse "refusing to adopt existing user or group ${name}"
		fi
	fi
}

check_runtime_dir() {
	if [[ $runtime_wanted != yes && $loaded_runtime != "$name" ]]; then
		return 0
	fi

	if ! exists "$runtime_dir"; then
		return 0
	fi

	if [[ ! -L $policy_link || $loaded_runtime != "$name" ]]; then
		refuse "runtime directory already in use: ${runtime_dir}"
	fi

	if [[ ! -d $runtime_dir || -L $runtime_dir || -z $uid || $(stat -c '%u:%g' -- "$runtime_dir") != "${uid}:${gid}" ]]; then
		refuse "unexpected runtime directory: ${runtime_dir}"
	fi
}

check_deployment
read_unit_state

if [[ $action == uninstall && ! -L $unit_link && ! -L $policy_link && ! -L $rotation_link ]]; then
	echo 'Already uninstalled.'
	exit 0
fi

check_identity
check_runtime_dir

for source in "$unit_file" "$policy" "${conf_dir}/${name}_logs.conf"; do
	if exists "$source"; then
		check_regular_file "$source"
		check_trusted_path "$source"
	fi
done

if [[ -L $policy_link ]]; then
	if [[ -n $uid ]] && ! command -v userdel >/dev/null; then
		refuse 'missing userdel'
	fi

	if [[ -n $gid ]] && ! command -v groupdel >/dev/null; then
		refuse 'missing groupdel'
	fi
fi

phase='uninstall (changes may already have been applied)'

if [[ $load_state == loaded && $active != inactive ]]; then
	systemctl stop "$unit"
fi

if [[ $enabled == enabled || $enabled == enabled-runtime ]]; then
	systemctl --no-reload disable "$unit"
fi

for link in "$unit_link" "$rotation_link"; do
	if [[ -L $link ]]; then
		rm -- "$link"
	fi
done

if [[ $load_state == loaded ]]; then
	systemctl daemon-reload
fi

# Keep the verified policy link until account removal succeeds so a retry
# can finish even if userdel succeeded and groupdel failed.
if [[ -L $policy_link ]]; then
	if [[ -n $uid ]]; then
		userdel "$name"
	fi

	# userdel can also remove the private group.
	if [[ -n $gid ]]; then
		remaining_group=$(lookup group "$name")
		if [[ -n $remaining_group ]]; then
			groupdel "$name"
		fi
	fi

	rm -- "$policy_link"
fi

echo 'Uninstall complete. Application files were left in place.'
