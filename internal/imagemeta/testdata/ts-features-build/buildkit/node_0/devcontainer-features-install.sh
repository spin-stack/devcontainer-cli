#!/bin/sh
set -e

on_exit () {
	[ $? -eq 0 ] && exit
	echo 'ERROR: Feature "Node.js" (ghcr.io/o/f/node) failed to install! Look at the documentation at https://x/doc for help troubleshooting this error.'
}

trap on_exit EXIT

echo ===========================================================================

echo 'Feature       : Node.js'
echo 'Description   : Installs '\''node'\'''
echo 'Id            : ghcr.io/o/f/node'
echo 'Version       : 1.2.3'
echo 'Documentation : https://x/doc'
echo 'Options       :'
echo '    VERSION="18"
    NVMVERSION="0.39"'
echo ===========================================================================

set -a
. ../devcontainer-features.builtin.env
. ./devcontainer-features.env
set +a

chmod +x ./install.sh
./install.sh
