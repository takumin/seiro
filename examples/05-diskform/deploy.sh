#!/bin/bash
# Run by cloud-init on the installer (the Ubuntu cloud image on vda).
# It builds the storage of diskform.yaml on the blank disks, installs Debian
# into it with debootstrap and makes the firmware boot it next. On success it
# leaves /run/deploy-ok, which lets cloud-init's power_state power off.
set -euxo pipefail

declaration=/etc/diskform/diskform.yaml
target=/target
user=ops

# The LUKS key. The deployed system unlocks its root with a copy kept in its
# initramfs, so that it boots unattended; /boot is not encrypted, so this
# only suits a test machine.
install -d -m 0700 /run/keys
head -c 64 /dev/urandom > /run/keys/sys.key
chmod 0400 /run/keys/sys.key

diskform apply --auto-approve "$declaration"
mkdir -p "$target"
diskform mount --root "$target" "$declaration"

debootstrap --variant=minbase \
	--include=systemd-sysv,udev,kmod,ca-certificates,e2fsprogs,cryptsetup,cryptsetup-initramfs,lvm2,btrfs-progs,xfsprogs,dosfstools,openssh-server,sudo,efibootmgr \
	trixie "$target" http://deb.debian.org/debian

# What the deployed system mounts and unlocks at boot, which diskform does
# not generate (ADR 0019 of diskform).
luks_device=$(cryptsetup status cryptsys | awk '$1 == "device:" { print $2 }')
luks_uuid=$(cryptsetup luksUUID "$luks_device")
cat > "$target/etc/fstab" << 'FSTAB'
/dev/mapper/vg0-root / ext4 defaults 0 1
LABEL=boot /boot ext4 defaults 0 2
LABEL=EFI /boot/efi vfat umask=0077 0 2
LABEL=var /var xfs defaults 0 2
LABEL=data /srv btrfs subvol=@srv,compress=zstd:3 0 0
LABEL=data /srv/.snapshots btrfs subvol=@snapshots,compress=zstd:3 0 0
LABEL=swap none swap sw 0 0
FSTAB
echo "cryptsys UUID=$luks_uuid /etc/keys/sys.key luks,discard" > "$target/etc/crypttab"
install -d -m 0700 "$target/etc/keys"
install -m 0400 /run/keys/sys.key "$target/etc/keys/sys.key"
echo 'KEYFILE_PATTERN="/etc/keys/*.key"' >> "$target/etc/cryptsetup-initramfs/conf-hook"
install -d "$target/etc/initramfs-tools/conf.d"
echo 'UMASK=0077' > "$target/etc/initramfs-tools/conf.d/umask"

echo deployed > "$target/etc/hostname"
install -d "$target/etc/default"
cat > "$target/etc/default/grub" << 'GRUB'
GRUB_DEFAULT=0
GRUB_TIMEOUT=1
GRUB_DISTRIBUTOR=Debian
GRUB_CMDLINE_LINUX_DEFAULT=""
GRUB_CMDLINE_LINUX="console=ttyS0"
GRUB_TERMINAL=serial
GRUB_SERIAL_COMMAND="serial --unit=0 --speed=115200"
GRUB
mkdir -p "$target/sys/firmware/efi/efivars"
for dir in dev dev/pts proc sys sys/firmware/efi/efivars run; do
	mount --bind "/$dir" "$target/$dir"
done
chroot "$target" apt-get update
chroot "$target" env DEBIAN_FRONTEND=noninteractive \
	apt-get install --yes --no-install-recommends linux-image-amd64 grub-efi-amd64
# grub-install adds the boot entry to the UEFI variables, which seiro keeps
# for machines run by QEMU, and puts it first in the boot order.
chroot "$target" grub-install --target=x86_64-efi --efi-directory=/boot/efi --bootloader-id=debian
chroot "$target" update-grub

# SSH over vsock as seiro set it up on the installer, so that `seiro ssh`
# reaches the deployed system with the same host key and user.
cp -a /etc/ssh/ssh_host_ed25519_key /etc/ssh/ssh_host_ed25519_key.pub "$target/etc/ssh/"
install -D -m 0644 /etc/modules-load.d/seiro-vsock.conf "$target/etc/modules-load.d/seiro-vsock.conf"
cp -a /etc/systemd/system/seiro-sshd-vsock.socket /etc/systemd/system/seiro-sshd-vsock@.service "$target/etc/systemd/system/"
chroot "$target" systemctl enable seiro-sshd-vsock.socket
# '*' rather than a locked password, so that sshd accepts the key.
chroot "$target" useradd --create-home --shell /bin/bash --password '*' "$user"
install -d -m 0700 "$target/home/$user/.ssh"
install -m 0600 "/home/$user/.ssh/authorized_keys" "$target/home/$user/.ssh/authorized_keys"
chroot "$target" chown -R "$user:$user" "/home/$user/.ssh"
echo "$user ALL=(ALL) NOPASSWD:ALL" > "$target/etc/sudoers.d/$user"
chmod 0440 "$target/etc/sudoers.d/$user"

# What the verification after the reboot reads.
install -m 0755 /usr/local/sbin/diskform "$target/usr/local/sbin/diskform"
install -d "$target/etc/diskform"
install -m 0644 "$declaration" "$target/etc/diskform/diskform.yaml"
echo deployed-by-diskform > "$target/srv/marker"

for dir in run sys/firmware/efi/efivars sys proc dev/pts dev; do
	umount "$target/$dir"
done
diskform unmount --root "$target" "$declaration"
touch /run/deploy-ok
