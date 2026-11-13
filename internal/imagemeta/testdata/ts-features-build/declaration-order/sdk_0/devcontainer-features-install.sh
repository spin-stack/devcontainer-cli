#!/bin/sh
set -e

on_exit () {
	[ $? -eq 0 ] && exit
	echo 'ERROR: Feature "./sdk" (Unknown) failed to install!'
}

trap on_exit EXIT

echo ===========================================================================

echo 'Feature       : ./sdk'
echo 'Description   : '
echo 'Id            : Unknown'
echo 'Version       : '
echo 'Documentation : '
echo 'Options       :'
echo '    VERSION="2"
    ZZ="1"
    AA="3"'
echo ===========================================================================

set -a
. ../devcontainer-features.builtin.env
. ./devcontainer-features.env
set +a

chmod +x ./install.sh
./install.sh
