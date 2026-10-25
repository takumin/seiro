#!/bin/bash
# Run as root on the deployed system once it has booted. It checks that the
# machine booted the Debian that deploy.sh installed, from the storage of
# diskform.yaml, and that diskform finds that storage configured as declared.
set -euo pipefail

fail()
{
	echo "FAIL: $*" >&2
	exit 1
}
check()
{
	local what=$1 got=$2 want=$3
	[[ $got == "$want" ]] || fail "$what: got '$got', want '$want'"
	echo "ok: $what: $got"
}

lsblk -o NAME,TYPE,SIZE,FSTYPE,LABEL,MOUNTPOINTS
findmnt --real
swapon --show
efibootmgr

# shellcheck source=/dev/null
. /etc/os-release
check "distribution" "$ID $VERSION_CODENAME" "debian trixie"
check "hostname" "$(cat /proc/sys/kernel/hostname)" "deployed"
current=$(efibootmgr | sed -n 's/^BootCurrent: //p')
check "boot entry" "$(efibootmgr | awk -v entry="Boot$current" '$1 == entry || $1 == entry "*" { sub(/^[^ ]+ /, ""); sub(/\t.*/, ""); print }')" "debian"

check "root" "$(findmnt -no SOURCE /)" "/dev/mapper/vg0-root"
check "root fstype" "$(findmnt -no FSTYPE /)" "ext4"
check "/boot" "$(findmnt -no LABEL /boot) $(findmnt -no FSTYPE /boot)" "boot ext4"
check "/boot/efi" "$(findmnt -no LABEL /boot/efi) $(findmnt -no FSTYPE /boot/efi)" "EFI vfat"
check "/var" "$(findmnt -no SOURCE /var) $(findmnt -no FSTYPE /var)" "/dev/mapper/vg0-var xfs"
check "/srv" "$(findmnt -no FSTYPE /srv) $(findmnt -no FSROOT /srv)" "btrfs /@srv"
check "/srv/.snapshots" "$(findmnt -no FSTYPE /srv/.snapshots) $(findmnt -no FSROOT /srv/.snapshots)" "btrfs /@snapshots"
check "swap" "$(swapon --show=NAME --noheadings | xargs -r readlink -f)" "$(readlink -f /dev/mapper/vg0-swap)"
check "LUKS" "$(lsblk -dno TYPE "$(readlink -f /dev/mapper/cryptsys)")" "crypt"
check "btrfs data profile" "$(btrfs filesystem df /srv | sed -n 's/^Data, \([^:]*\):.*/\1/p')" "RAID1"
check "btrfs metadata profile" "$(btrfs filesystem df /srv | sed -n 's/^Metadata, \([^:]*\):.*/\1/p')" "RAID1"
check "btrfs devices" "$(btrfs filesystem show --raw /srv | grep -c 'devid')" "2"
check "marker" "$(cat /srv/marker)" "deployed-by-diskform"

# diskform judges every group configured as declared, so it would change
# nothing. Its LUKS keyfile is where the declaration names it.
install -d -m 0700 /run/keys
install -m 0400 /etc/keys/sys.key /run/keys/sys.key
plan=$(diskform plan /etc/diskform/diskform.yaml)
echo "$plan"
grep -qx 'already configured: disk.sys0' <<< "$plan" || fail "disk.sys0 is not configured as declared"
grep -qx 'already configured: disk.data0, disk.data1' <<< "$plan" || fail "disk.data0 and disk.data1 are not configured as declared"
if grep -q '^operations:' <<< "$plan"; then
	fail "diskform plans operations"
fi
echo "ok: diskform plan"
