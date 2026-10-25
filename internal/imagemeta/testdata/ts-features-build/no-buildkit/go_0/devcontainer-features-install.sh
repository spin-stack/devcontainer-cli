#!/bin/sh
set -e

on_exit () {
	[ $? -eq 0 ] && exit
	echo 'ERROR: Feature "Go" (ghcr.io/devcontainers/features/go) failed to install!'
}

trap on_exit EXIT

echo ===========================================================================

echo 'Feature       : Go'
echo 'Description   : '
echo 'Id            : ghcr.io/devcontainers/features/go'
echo 'Version       : 1.3.0'
echo 'Documentation : '
echo 'Options       :'
echo '    VERSION="true"
    GOLANGCILINTVERSION="latest"'
echo ===========================================================================

set -a
. ../devcontainer-features.builtin.env
. ./devcontainer-features.env
set +a

chmod +x ./install.sh
./install.sh
