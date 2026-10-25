#!/bin/sh
# Run on the machine while waiting for the deployment. It prints `deployed`
# on the deployed Debian, `rebooting` on the installer once deploy.sh has
# succeeded, and otherwise the status of cloud-init on the installer.
# shellcheck source=/dev/null
. /etc/os-release
if [ "$ID" = debian ]; then
	echo deployed
elif [ -f /run/deploy-ok ]; then
	echo rebooting
else
	cloud-init status | sed -n 's/^status: //p'
fi
