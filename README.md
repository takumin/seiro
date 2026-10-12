# seiro

Virtual machine environments declared in Starlark, powered by cloud-hypervisor and passt

[![CI](https://github.com/takumin/seiro/actions/workflows/ci.yml/badge.svg)](https://github.com/takumin/seiro/actions/workflows/ci.yml)
[![Coverage](https://raw.githubusercontent.com/takumin/octocov-central/main/badges/takumin/seiro/coverage.svg)](https://github.com/takumin/seiro/actions/workflows/ci.yml)
[![Code to Test Ratio](https://raw.githubusercontent.com/takumin/octocov-central/main/badges/takumin/seiro/ratio.svg)](https://github.com/takumin/seiro/actions/workflows/ci.yml)
[![Test Execution Time](https://raw.githubusercontent.com/takumin/octocov-central/main/badges/takumin/seiro/time.svg)](https://github.com/takumin/seiro/actions/workflows/ci.yml)

## Overview

seiro creates virtual machines from a `Seirofile` written in [Starlark](https://github.com/bazelbuild/starlark).
Machines run on [cloud-hypervisor](https://www.cloudhypervisor.org/) and reach the outside world through [passt](https://passt.top/), without root privileges.

```python
ubuntu = image(
    url = "https://cloud-images.ubuntu.com/releases/26.04/release-20260927/ubuntu-26.04-server-cloudimg-amd64.img",
    sha256 = "8800651811af9a85465ad1d552add729947bb16488dddb4a9b5305a3d97332b2",
)

machine(
    name = "dev",
    cpus = 2,
    memory = "2GiB",
    disks = [disk(image = ubuntu, size = "20GiB")],
    nics = [nic("eth0", network = internet)],
    ports = [port(host = 2222, guest = 22)],
    cloud_init = cloud_init(user_data = {
        "users": [{"name": "dev", "ssh_authorized_keys": [file("~/.ssh/id_ed25519.pub")]}],
    }),
)
```

```sh
seiro up              # create and start machines
seiro ssh dev         # log in over vsock (no published port needed)
ssh -p 2222 dev@localhost
seiro status          # show machine states
seiro console dev     # attach to the serial console (Ctrl-] to detach)
seiro logs -f dev     # follow the serial output
seiro down            # power off machines
seiro destroy         # delete machines and their disks
```

## SSH over vsock

Every machine with `cloud_init` gets sshd listening on vsock, so `seiro ssh` works without `ports` or even a NIC.

```sh
seiro ssh dev                       # as the first user created by cloud_init, or $USER
seiro ssh root@dev                  # as another user
seiro ssh dev -L 8080:localhost:80  # arguments after the machine go to ssh
seiro ssh dev uptime                # run a command
seiro ssh-config >> ~/.ssh/config   # make `ssh dev`, scp, rsync and editors work too
```

seiro generates the machine's SSH host key and passes it to cloud-init, so the host key is verified from the first connection.
Your `authorized_keys` still come from `cloud_init`.
The guest needs systemd and OpenSSH; seiro adds a socket unit that listens on vsock port 10022.

## Tweaks

seiro adjusts how the guest boots so that stock cloud images work well.
The image itself is never modified: the adjustments are passed to systemd in the guest at boot,
so they take effect on systemd 256 or later (e.g. Debian 13, Ubuntu 26.04) and older guests simply ignore them.

| Tweak | Effect |
| --- | --- |
| `early_nic_names` | NICs get their declared names as soon as they appear, even in the initrd. Without it, an initrd that configures the network (dracut on Ubuntu 26.04) keeps cloud-init from renaming the NIC. |
| `skip_network_wait` | A machine without `nics` does not wait about two minutes for the network on every boot. |

All tweaks are on by default. Turn them off for images that take care of these themselves:

```python
machine(name = "custom", disks = [...], tweaks = tweaks(early_nic_names = False))
machine(name = "bare", disks = [...], tweaks = False)  # all off
```

## Requirements

- Linux with KVM (`/dev/kvm` accessible by your user)
- `cloud-hypervisor` and `passt` in `PATH` (`aqua i` installs both)

Images and the firmware are downloaded once and cached under `$XDG_CACHE_HOME/seiro`.
Machine state lives under `$XDG_STATE_HOME/seiro`.

## Installation

```sh
task install          # build for the host and install to ~/.local/bin/seiro
task install:uninstall
```

Set `INSTALL_DIR` to install somewhere else, e.g. `INSTALL_DIR=~/bin task install`.

### Shell completion

`seiro completion <shell>` prints a completion script for bash, zsh, fish, or PowerShell.
Machine names declared in the Seirofile are completed for `up`, `down`, `destroy`, `console` and `logs`.

```sh
source <(seiro completion bash)   # ~/.bashrc
source <(seiro completion zsh)    # ~/.zshrc
seiro completion fish > ~/.config/fish/completions/seiro.fish
```
